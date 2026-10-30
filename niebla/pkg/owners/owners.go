package owners

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config represents a parsed OWNERS.toml configuration file.
type Config struct {
	SetNoParent bool                `toml:"set_noparent"`
	Approvers   []string            `toml:"approvers"`
	Reviewers   []string            `toml:"reviewers"`
	PerFile     map[string][]string `toml:"per_file"`
}

// ParseTOML parses raw TOML data into an Owners Config.
func ParseTOML(data []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse OWNERS.toml: %w", err)
	}

	// Normalize approvers and reviewers
	cfg.Approvers = cleanStringSlice(cfg.Approvers)
	cfg.Reviewers = cleanStringSlice(cfg.Reviewers)
	if cfg.PerFile != nil {
		cleanedPerFile := make(map[string][]string, len(cfg.PerFile))
		for pattern, owners := range cfg.PerFile {
			cleanedPerFile[pattern] = cleanStringSlice(owners)
		}
		cfg.PerFile = cleanedPerFile
	}

	return &cfg, nil
}

func cleanStringSlice(slice []string) []string {
	var res []string
	for _, s := range slice {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}

// GroupResolver resolves an enterprise group name (e.g. "@backend") into a list of member usernames.
type GroupResolver func(group string) []string

// FileProvider retrieves raw bytes of OWNERS.toml for a given repository-relative directory path.
// dir is "" for repo root, or "pkg/auth", "cmd/server".
// Returns nil, nil (or os.ErrNotExist) if no OWNERS.toml exists in that directory.
type FileProvider func(dir string) ([]byte, error)

// Requirement represents an approval requirement for a specific touched file path.
type Requirement struct {
	Path       string   `json:"path"`
	Directory  string   `json:"directory"`
	Rule       string   `json:"rule"`
	Approvers  []string `json:"approvers"`
	Satisfied  bool     `json:"satisfied"`
	ApprovedBy string   `json:"approved_by,omitempty"`
}

// EvaluationResult aggregates whether all touched paths have valid owner approvals.
type EvaluationResult struct {
	Satisfied  bool          `json:"satisfied"`
	TotalPaths int           `json:"total_paths"`
	Approved   []Requirement `json:"approved,omitempty"`
	Missing    []Requirement `json:"missing,omitempty"`
}

// Evaluator evaluates changelist file diffs against hierarchical OWNERS.toml files.
type Evaluator struct {
	provider      FileProvider
	groupResolver GroupResolver
	cache         map[string]*Config
}

// NewEvaluator creates a new Evaluator.
func NewEvaluator(provider FileProvider, groupResolver GroupResolver) *Evaluator {
	return &Evaluator{
		provider:      provider,
		groupResolver: groupResolver,
		cache:         make(map[string]*Config),
	}
}

// getConfig retrieves and parses OWNERS.toml for dir (using in-memory cache).
func (e *Evaluator) getConfig(dir string) (*Config, error) {
	if cfg, ok := e.cache[dir]; ok {
		return cfg, nil
	}

	if e.provider == nil {
		return nil, nil
	}

	data, err := e.provider(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			e.cache[dir] = nil
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		e.cache[dir] = nil
		return nil, nil
	}

	cfg, err := ParseTOML(data)
	if err != nil {
		return nil, fmt.Errorf("error in %s/OWNERS.toml: %w", dir, err)
	}

	e.cache[dir] = cfg
	return cfg, nil
}

// ResolveRequirements resolves the set of approvers for each touched file path by traversing directory hierarchy.
func (e *Evaluator) ResolveRequirements(touchedPaths []string) ([]Requirement, error) {
	var requirements []Requirement

	for _, p := range touchedPaths {
		cleanPath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "/")
		if cleanPath == "" || cleanPath == "." {
			continue
		}

		req, err := e.resolvePath(cleanPath)
		if err != nil {
			return nil, err
		}
		if req != nil {
			requirements = append(requirements, *req)
		}
	}

	return requirements, nil
}

// resolvePath traverses up from the file's directory to repo root collecting approvers.
func (e *Evaluator) resolvePath(cleanPath string) (*Requirement, error) {
	currDir := path.Dir(cleanPath)
	if currDir == "." {
		currDir = ""
	}

	fileName := path.Base(cleanPath)
	var accumulatedApprovers []string
	rule := "default"
	governingDir := ""
	foundAnyConfig := false

	dir := currDir
	for {
		cfg, err := e.getConfig(dir)
		if err != nil {
			return nil, err
		}

		if cfg != nil {
			foundAnyConfig = true
			if governingDir == "" {
				governingDir = dir
			}

			// 1. Check per-file pattern match in this directory
			var perFileMatched bool
			for pattern, owners := range cfg.PerFile {
				matched, matchErr := path.Match(pattern, fileName)
				if matchErr == nil && matched {
					perFileMatched = true
					rule = fmt.Sprintf("per_file: %s", pattern)
					accumulatedApprovers = append(accumulatedApprovers, owners...)
					break
				}
			}

			// 2. If no per-file pattern matched, collect directory approvers
			if !perFileMatched && len(cfg.Approvers) > 0 {
				accumulatedApprovers = append(accumulatedApprovers, cfg.Approvers...)
			}

			// 3. If set_noparent is true, stop ascending
			if cfg.SetNoParent {
				break
			}
		}

		if dir == "" {
			break
		}
		parentDir := path.Dir(dir)
		if parentDir == "." || parentDir == dir {
			dir = ""
		} else {
			dir = parentDir
		}
	}

	if !foundAnyConfig || len(accumulatedApprovers) == 0 {
		return nil, nil
	}

	uniqueApprovers := deduplicate(accumulatedApprovers)
	return &Requirement{
		Path:      cleanPath,
		Directory: governingDir,
		Rule:      rule,
		Approvers: uniqueApprovers,
	}, nil
}

// Evaluate checks whether the provided approvals satisfy all required owners for the touched paths.
func (e *Evaluator) Evaluate(touchedPaths []string, approvedBy []string) (*EvaluationResult, error) {
	requirements, err := e.ResolveRequirements(touchedPaths)
	if err != nil {
		return nil, err
	}

	result := &EvaluationResult{
		Satisfied:  true,
		TotalPaths: len(touchedPaths),
	}

	// Normalize approved usernames
	approvalsMap := make(map[string]bool)
	for _, a := range approvedBy {
		trimmed := strings.ToLower(strings.TrimSpace(a))
		if trimmed != "" {
			approvalsMap[trimmed] = true
		}
	}

	for _, req := range requirements {
		satisfied := false
		var matchedUser string

		for _, owner := range req.Approvers {
			ownerLower := strings.ToLower(owner)

			// 1. Direct user match
			if !strings.HasPrefix(ownerLower, "@") {
				if approvalsMap[ownerLower] {
					satisfied = true
					matchedUser = owner
					break
				}
			} else {
				// 2. Enterprise group resolution
				if approvalsMap[ownerLower] {
					satisfied = true
					matchedUser = owner
					break
				}
				if e.groupResolver != nil {
					members := e.groupResolver(owner)
					for _, m := range members {
						if approvalsMap[strings.ToLower(strings.TrimSpace(m))] {
							satisfied = true
							matchedUser = m
							break
						}
					}
					if satisfied {
						break
					}
				}
			}
		}

		req.Satisfied = satisfied
		req.ApprovedBy = matchedUser

		if satisfied {
			result.Approved = append(result.Approved, req)
		} else {
			result.Satisfied = false
			result.Missing = append(result.Missing, req)
		}
	}

	return result, nil
}

func deduplicate(items []string) []string {
	seen := make(map[string]bool)
	var res []string
	for _, it := range items {
		lower := strings.ToLower(it)
		if !seen[lower] {
			seen[lower] = true
			res = append(res, it)
		}
	}
	return res
}
