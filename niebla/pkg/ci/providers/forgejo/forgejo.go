package forgejo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"niebla/pkg/ci"
	"niebla/pkg/ci/auth"
)

// Config specifies the configuration for Forgejo / Gitea Actions workflow dispatch.
type Config struct {
	Name     string
	Endpoint string // e.g. https://git.example.com/api/v1/repos/{owner}/{repo}/actions/workflows/{workflow_id}/dispatches
	Ref      string // git ref to trigger on (e.g. "main")
	Auth     auth.AuthConfig
	Timeout  time.Duration
}

// Provider implements ci.Provider for Forgejo / Gitea Actions.
type Provider struct {
	cfg    Config
	client *http.Client
}

// NewProvider creates a new Forgejo workflow dispatch provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = "forgejo"
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("endpoint cannot be empty for forgejo runner %q", cfg.Name)
	}
	if cfg.Ref == "" {
		cfg.Ref = "main"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}

	client, err := auth.NewClient(cfg.Timeout, cfg.Auth)
	if err != nil {
		return nil, fmt.Errorf("failed to create forgejo auth client: %w", err)
	}

	return &Provider{
		cfg:    cfg,
		client: client,
	}, nil
}

func (p *Provider) Name() string {
	return p.cfg.Name
}

type dispatchPayload struct {
	Ref    string            `json:"ref"`
	Inputs map[string]string `json:"inputs"`
}

// Trigger sends a workflow dispatch request to the Forgejo API.
func (p *Provider) Trigger(ctx context.Context, req ci.DispatchRequest) (string, error) {
	payload := dispatchPayload{
		Ref: p.cfg.Ref,
		Inputs: map[string]string{
			"cl_id":        strconv.FormatInt(req.CLID, 10),
			"patchset":     strconv.Itoa(req.Patchset),
			"tree_hash":    req.TreeHash,
			"author":       req.Author,
			"server_url":   req.ServerURL,
			"runner_token": req.RunnerToken,
		},
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to encode forgejo dispatch payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create forgejo request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("forgejo dispatch request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("forgejo dispatch failed with HTTP status %d", resp.StatusCode)
	}

	return p.cfg.Endpoint, nil
}
