package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"niebla/internal/config"
)

// Resolver resolves group identifiers into individual usernames.
type Resolver interface {
	ResolveGroup(ctx context.Context, group string) ([]string, error)
	GetUserGroups(ctx context.Context, username string) ([]string, error)
	IsGroup(candidate string) bool
}

// NewResolver constructs a Resolver based on the CritiqueGroupConfig.
func NewResolver(cfg config.CritiqueGroupConfig) Resolver {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case "api":
		return &APIResolver{
			URL:       cfg.API.URL,
			TokenEnv:  cfg.API.TokenEnv,
			TimeoutMS: cfg.API.TimeoutMS,
		}
	case "disabled":
		return &DisabledResolver{}
	case "static", "":
		fallthrough
	default:
		return &StaticResolver{
			Groups: cfg.Static,
		}
	}
}

// DisabledResolver does not resolve any groups.
type DisabledResolver struct{}

func (d *DisabledResolver) ResolveGroup(ctx context.Context, group string) ([]string, error) {
	return nil, nil
}

func (d *DisabledResolver) GetUserGroups(ctx context.Context, username string) ([]string, error) {
	return nil, nil
}

func (d *DisabledResolver) IsGroup(candidate string) bool {
	return false
}

// StaticResolver resolves groups from an in-memory or config map.
type StaticResolver struct {
	Groups map[string][]string
}

func normalizeGroupName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "@")
	return strings.ToLower(name)
}

func (s *StaticResolver) IsGroup(candidate string) bool {
	if s.Groups == nil {
		return false
	}
	clean := normalizeGroupName(candidate)
	_, ok := s.Groups[clean]
	if !ok {
		_, ok = s.Groups["@"+clean]
	}
	return ok
}

func (s *StaticResolver) ResolveGroup(ctx context.Context, group string) ([]string, error) {
	if s.Groups == nil {
		return nil, nil
	}
	clean := normalizeGroupName(group)
	if members, ok := s.Groups[clean]; ok {
		return members, nil
	}
	if members, ok := s.Groups["@"+clean]; ok {
		return members, nil
	}
	return nil, nil
}

func (s *StaticResolver) GetUserGroups(ctx context.Context, username string) ([]string, error) {
	if s.Groups == nil {
		return nil, nil
	}
	uClean := strings.ToLower(strings.TrimSpace(username))
	var userGroups []string
	for grp, members := range s.Groups {
		for _, m := range members {
			if strings.ToLower(strings.TrimSpace(m)) == uClean {
				userGroups = append(userGroups, grp)
				break
			}
		}
	}
	return userGroups, nil
}

// APIResolver queries a remote HTTP API for group member expansion.
type APIResolver struct {
	URL       string
	TokenEnv  string
	TimeoutMS int
	Client    *http.Client
}

func (a *APIResolver) getClient() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	timeout := time.Duration(a.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

func (a *APIResolver) IsGroup(candidate string) bool {
	return strings.HasPrefix(strings.TrimSpace(candidate), "@")
}

func (a *APIResolver) ResolveGroup(ctx context.Context, group string) ([]string, error) {
	clean := normalizeGroupName(group)
	reqURL := fmt.Sprintf("%s/%s/members", strings.TrimRight(a.URL, "/"), clean)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create group api request: %w", err)
	}

	if a.TokenEnv != "" {
		if token := os.Getenv(a.TokenEnv); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	req.Header.Set("Accept", "application/json")

	resp, err := a.getClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("group API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("group API returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var data struct {
		Members []string `json:"members"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode group API response: %w", err)
	}

	return data.Members, nil
}

func (a *APIResolver) GetUserGroups(ctx context.Context, username string) ([]string, error) {
	return nil, nil
}

// ExpandReviewers takes raw reviewer input strings (e.g. "alice, bob, @backend"),
// splits comma-separated entries, trims whitespace, resolves any group members,
// and returns a deduplicated, sorted list of usernames.
func ExpandReviewers(ctx context.Context, resolver Resolver, inputs []string) ([]string, error) {
	seen := make(map[string]bool)
	var result []string

	for _, input := range inputs {
		parts := strings.Split(input, ",")
		for _, part := range parts {
			item := strings.TrimSpace(part)
			if item == "" {
				continue
			}

			// Check if item is a group
			isGroup := strings.HasPrefix(item, "@") || (resolver != nil && resolver.IsGroup(item))
			if isGroup && resolver != nil {
				members, err := resolver.ResolveGroup(ctx, item)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve group %q: %w", item, err)
				}
				if len(members) > 0 {
					for _, m := range members {
						mTrim := strings.TrimSpace(m)
						if mTrim != "" && !seen[mTrim] {
							seen[mTrim] = true
							result = append(result, mTrim)
						}
					}
					continue
				}
			}

			// Individual user or unresolved name: strip optional @ and append
			cleanUser := strings.TrimPrefix(item, "@")
			if cleanUser != "" && !seen[cleanUser] {
				seen[cleanUser] = true
				result = append(result, cleanUser)
			}
		}
	}

	sort.Strings(result)
	return result, nil
}
