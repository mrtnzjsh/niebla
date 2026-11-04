package owners

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// PerFileRule represents a pattern-scoped ownership and governance rule inside [per_file].
// In OWNERS.toml, a [per_file] entry can be either a simple list of approver strings:
//
//	"*.proto" = ["@api-governance", "grace"]
//
// or an inline table with granular overrides:
//
//	"*.templ" = { approvers = ["@design-system"], set_noparent = true, required_approvals = 2, agents = ["a11y-bot"] }
type PerFileRule struct {
	SetNoParent             bool     `toml:"set_noparent" json:"set_noparent,omitempty"`
	Approvers               []string `toml:"approvers" json:"approvers,omitempty"`
	Reviewers               []string `toml:"reviewers" json:"reviewers,omitempty"`
	RequiredApprovals       *int     `toml:"required_approvals" json:"required_approvals,omitempty"`
	RequireGreenCI          *bool    `toml:"require_green_ci" json:"require_green_ci,omitempty"`
	AllowUnresolvedComments *bool    `toml:"allow_unresolved_comments" json:"allow_unresolved_comments,omitempty"`
	Agents                  []string `toml:"agents" json:"agents,omitempty"`
	PresubmitHooks          []string `toml:"presubmit_hooks" json:"presubmit_hooks,omitempty"`
	CIRunners               []string `toml:"ci_runners" json:"ci_runners,omitempty"`
}

// Config represents a parsed OWNERS.toml configuration file.
type Config struct {
	SetNoParent             bool                   `toml:"set_noparent"`
	Approvers               []string               `toml:"approvers"`
	Reviewers               []string               `toml:"reviewers"`
	RequiredApprovals       *int                   `toml:"required_approvals"`
	RequireGreenCI          *bool                  `toml:"require_green_ci"`
	AllowUnresolvedComments *bool                  `toml:"allow_unresolved_comments"`
	Agents                  []string               `toml:"agents"`
	PresubmitHooks          []string               `toml:"presubmit_hooks"`
	CIRunners               []string               `toml:"ci_runners"`
	PerFile                 map[string][]string    `toml:"-"`
	PerFileRules            map[string]PerFileRule `toml:"-"`
}

type rawOwnersConfig struct {
	SetNoParent             bool           `toml:"set_noparent"`
	Approvers               []string       `toml:"approvers"`
	Reviewers               []string       `toml:"reviewers"`
	RequiredApprovals       *int           `toml:"required_approvals"`
	RequireGreenCI          *bool          `toml:"require_green_ci"`
	AllowUnresolvedComments *bool          `toml:"allow_unresolved_comments"`
	Agents                  []string       `toml:"agents"`
	PresubmitHooks          []string       `toml:"presubmit_hooks"`
	CIRunners               []string       `toml:"ci_runners"`
	PerFile                 map[string]any `toml:"per_file"`
}

// ParseTOML parses raw TOML data into an Owners Config.
func ParseTOML(data []byte) (*Config, error) {
	var raw rawOwnersConfig
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse OWNERS.toml: %w", err)
	}

	cfg := Config{
		SetNoParent:             raw.SetNoParent,
		Approvers:               cleanStringSlice(raw.Approvers),
		Reviewers:               cleanStringSlice(raw.Reviewers),
		RequiredApprovals:       raw.RequiredApprovals,
		RequireGreenCI:          raw.RequireGreenCI,
		AllowUnresolvedComments: raw.AllowUnresolvedComments,
		Agents:                  cleanStringSlice(raw.Agents),
		PresubmitHooks:          cleanStringSlice(raw.PresubmitHooks),
		CIRunners:               cleanStringSlice(raw.CIRunners),
	}

	if raw.PerFile != nil {
		cfg.PerFile = make(map[string][]string, len(raw.PerFile))
		cfg.PerFileRules = make(map[string]PerFileRule, len(raw.PerFile))
		for pattern, rawVal := range raw.PerFile {
			rule, err := parsePerFileEntry(pattern, rawVal)
			if err != nil {
				return nil, err
			}
			cfg.PerFile[pattern] = rule.Approvers
			cfg.PerFileRules[pattern] = rule
		}
	}

	return &cfg, nil
}

func parsePerFileEntry(pattern string, rawVal any) (PerFileRule, error) {
	switch v := rawVal.(type) {
	case []any:
		var approvers []string
		for _, item := range v {
			str, ok := item.(string)
			if !ok {
				return PerFileRule{}, fmt.Errorf("failed to parse OWNERS.toml: per_file[%q] array elements must be strings", pattern)
			}
			approvers = append(approvers, str)
		}
		cleaned := cleanStringSlice(approvers)
		return PerFileRule{Approvers: cleaned}, nil
	case []string:
		cleaned := cleanStringSlice(v)
		return PerFileRule{Approvers: cleaned}, nil
	case map[string]any:
		// Re-marshal the inline table and unmarshal into PerFileRule for strict typing
		buf, err := toml.Marshal(v)
		if err != nil {
			return PerFileRule{}, fmt.Errorf("failed to parse OWNERS.toml per_file[%q]: %w", pattern, err)
		}
		var rule PerFileRule
		if err := toml.Unmarshal(buf, &rule); err != nil {
			return PerFileRule{}, fmt.Errorf("failed to parse OWNERS.toml per_file[%q]: %w", pattern, err)
		}
		rule.Approvers = cleanStringSlice(rule.Approvers)
		rule.Reviewers = cleanStringSlice(rule.Reviewers)
		rule.Agents = cleanStringSlice(rule.Agents)
		rule.PresubmitHooks = cleanStringSlice(rule.PresubmitHooks)
		rule.CIRunners = cleanStringSlice(rule.CIRunners)
		return rule, nil
	default:
		return PerFileRule{}, fmt.Errorf("failed to parse OWNERS.toml: per_file[%q] must be an array of strings or an inline table", pattern)
	}
}

func cleanStringSlice(slice []string) []string {
	var res []string
	for _, item := range slice {
		trimmed := strings.TrimSpace(item)
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

// SubtreePolicy represents the aggregated governance policies, reviewers, agents, hooks,
// and CI runners resolved across all touched subtrees in a changelist.
type SubtreePolicy struct {
	RequiredApprovals       *int     `json:"required_approvals,omitempty"`
	RequireGreenCI          *bool    `json:"require_green_ci,omitempty"`
	AllowUnresolvedComments *bool    `json:"allow_unresolved_comments,omitempty"`
	Reviewers               []string `json:"reviewers,omitempty"`
	Agents                  []string `json:"agents,omitempty"`
	PresubmitHooks          []string `json:"presubmit_hooks,omitempty"`
	CIRunners               []string `json:"ci_runners,omitempty"`
}

// EvaluationResult aggregates whether all touched paths have valid owner approvals
// and the effective subtree governance policy across all touched directories.
type EvaluationResult struct {
	Satisfied  bool          `json:"satisfied"`
	TotalPaths int           `json:"total_paths"`
	Approved   []Requirement `json:"approved,omitempty"`
	Missing    []Requirement `json:"missing,omitempty"`
	Policy     SubtreePolicy `json:"policy"`
}

// Evaluator evaluates changelist file diffs against hierarchical OWNERS.toml files.
type Evaluator struct {
	provider      FileProvider
	groupResolver GroupResolver
	cache         map[string]*Config
}

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

	for _, touchedPath := range touchedPaths {
		cleanPath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(touchedPath)), "/")
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

func parentDirectory(dir string) string {
	if dir == "" {
		return ""
	}
	parent := path.Dir(dir)
	if parent == "." || parent == dir {
		return ""
	}
	return parent
}

func matchPerFileApprovers(cfg *Config, fileName string) (matched bool, stopParent bool, rule string, approvers []string) {
	patterns := make([]string, 0, len(cfg.PerFile))
	for pattern := range cfg.PerFile {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)

	for _, pattern := range patterns {
		owners := cfg.PerFile[pattern]
		if ok, err := path.Match(pattern, fileName); err == nil && ok {
			matched = true
			rule = fmt.Sprintf("per_file: %s", pattern)
			approvers = append(approvers, owners...)
			if pfRule, ok := cfg.PerFileRules[pattern]; ok && pfRule.SetNoParent {
				stopParent = true
			}
			break
		}
	}
	return
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

			matched, pfStop, pfRule, pfApprovers := matchPerFileApprovers(cfg, fileName)
			if matched {
				rule = pfRule
				accumulatedApprovers = append(accumulatedApprovers, pfApprovers...)
			}

			if (!matched || len(accumulatedApprovers) == 0) && len(cfg.Approvers) > 0 {
				accumulatedApprovers = append(accumulatedApprovers, cfg.Approvers...)
			}

			if pfStop || cfg.SetNoParent {
				break
			}
		}

		if dir == "" {
			break
		}
		dir = parentDirectory(dir)
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

type policyCollector struct {
	policy    *SubtreePolicy
	reviewers []string
	agents    []string
	hooks     []string
	runners   []string
}

func (c *policyCollector) mergeRules(
	reqApprovals *int,
	requireGreenCI *bool,
	allowUnresolved *bool,
	revs, ags, hks, rns []string,
) {
	if reqApprovals != nil {
		if c.policy.RequiredApprovals == nil || *reqApprovals > *c.policy.RequiredApprovals {
			val := *reqApprovals
			c.policy.RequiredApprovals = &val
		}
	}
	if requireGreenCI != nil {
		if c.policy.RequireGreenCI == nil {
			val := *requireGreenCI
			c.policy.RequireGreenCI = &val
		} else if *requireGreenCI && !*c.policy.RequireGreenCI {
			required := true
			c.policy.RequireGreenCI = &required
		}
	}
	if allowUnresolved != nil {
		if c.policy.AllowUnresolvedComments == nil {
			unresolvedVal := *allowUnresolved
			c.policy.AllowUnresolvedComments = &unresolvedVal
		} else if !*allowUnresolved && *c.policy.AllowUnresolvedComments {
			disallowed := false
			c.policy.AllowUnresolvedComments = &disallowed
		}
	}
	c.reviewers = append(c.reviewers, revs...)
	c.agents = append(c.agents, ags...)
	c.hooks = append(c.hooks, hks...)
	c.runners = append(c.runners, rns...)
	if len(rns) > 0 {
		required := true
		c.policy.RequireGreenCI = &required
	}
}

func (c *policyCollector) applyPerFileRules(cfg *Config, fileName string) bool {
	var stopParent bool
	patterns := make([]string, 0, len(cfg.PerFileRules))
	for pattern := range cfg.PerFileRules {
		patterns = append(patterns, pattern)
	}
	sort.Strings(patterns)

	for _, pattern := range patterns {
		if matched, err := path.Match(pattern, fileName); err == nil && matched {
			pfRule := cfg.PerFileRules[pattern]
			c.mergeRules(
				pfRule.RequiredApprovals,
				pfRule.RequireGreenCI,
				pfRule.AllowUnresolvedComments,
				pfRule.Reviewers,
				pfRule.Agents,
				pfRule.PresubmitHooks,
				pfRule.CIRunners,
			)
			if pfRule.SetNoParent {
				stopParent = true
			}
		}
	}
	return stopParent
}

func (c *policyCollector) applyConfig(cfg *Config, fileName string) bool {
	stopParent := c.applyPerFileRules(cfg, fileName)
	c.mergeRules(
		cfg.RequiredApprovals,
		cfg.RequireGreenCI,
		cfg.AllowUnresolvedComments,
		cfg.Reviewers,
		cfg.Agents,
		cfg.PresubmitHooks,
		cfg.CIRunners,
	)
	return stopParent || cfg.SetNoParent
}

func (c *policyCollector) finalize() {
	c.policy.Reviewers = deduplicate(c.reviewers)
	c.policy.Agents = deduplicate(c.agents)
	c.policy.PresubmitHooks = deduplicate(c.hooks)
	c.policy.CIRunners = deduplicate(c.runners)
}

// ResolvePolicy resolves and aggregates subtree governance policies, reviewers, agents,
// presubmit hooks, and CI runners across all touched file paths in a changelist.
//
// Aggregation rules:
//   - RequiredApprovals: maximum across all touched OWNERS.toml chains and matching [per_file] rules.
//   - RequireGreenCI: true if any touched OWNERS.toml / [per_file] sets require_green_ci = true or specifies ci_runners.
//   - AllowUnresolvedComments: false (strictest wins) if any touched OWNERS.toml / [per_file] sets false.
//   - Reviewers, Agents, PresubmitHooks, CIRunners: deduplicated union across all touched hierarchies (stopping at set_noparent = true).
func (e *Evaluator) ResolvePolicy(touchedPaths []string) (*SubtreePolicy, error) {
	col := &policyCollector{policy: &SubtreePolicy{}}

	for _, touchedPath := range touchedPaths {
		cleanPath := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(touchedPath)), "/")
		if cleanPath == "" || cleanPath == "." {
			continue
		}

		currDir := path.Dir(cleanPath)
		if currDir == "." {
			currDir = ""
		}
		fileName := path.Base(cleanPath)

		dir := currDir
		for {
			cfg, err := e.getConfig(dir)
			if err != nil {
				return nil, err
			}

			if cfg != nil && col.applyConfig(cfg, fileName) {
				break
			}

			if dir == "" {
				break
			}
			dir = parentDirectory(dir)
		}
	}

	col.finalize()
	return col.policy, nil
}

// Evaluate checks whether the provided approvals satisfy all required owners for the touched paths
// and resolves the aggregated subtree governance policy.
func (e *Evaluator) Evaluate(touchedPaths []string, approvedBy []string) (*EvaluationResult, error) {
	requirements, err := e.ResolveRequirements(touchedPaths)
	if err != nil {
		return nil, err
	}
	policy, err := e.ResolvePolicy(touchedPaths)
	if err != nil {
		return nil, err
	}

	result := &EvaluationResult{
		Satisfied:  true,
		TotalPaths: len(touchedPaths),
	}
	if policy != nil {
		result.Policy = *policy
	}

	approvalsMap := make(map[string]bool)
	for _, approver := range approvedBy {
		trimmed := strings.ToLower(strings.TrimSpace(approver))
		if trimmed != "" {
			approvalsMap[trimmed] = true
		}
	}

	for _, req := range requirements {
		satisfied := false
		var matchedUser string

		for _, owner := range req.Approvers {
			ownerLower := strings.ToLower(owner)

			if !strings.HasPrefix(ownerLower, "@") {
				if approvalsMap[ownerLower] {
					satisfied = true
					matchedUser = owner
					break
				}
			} else {
				if approvalsMap[ownerLower] {
					satisfied = true
					matchedUser = owner
					break
				}
				if e.groupResolver != nil {
					members := e.groupResolver(owner)
					for _, member := range members {
						if approvalsMap[strings.ToLower(strings.TrimSpace(member))] {
							satisfied = true
							matchedUser = member
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
