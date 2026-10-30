package hooks

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config represents the top-level schema of .niebla/hooks.toml.
type Config struct {
	Client ClientConfig `toml:"client"`
	Server ServerConfig `toml:"server"`
}

// ClientConfig holds client-side presubmit hooks executed before gRPC dispatch.
type ClientConfig struct {
	Presubmit []ClientHook `toml:"presubmit"`
}

// ServerConfig holds server-side presubmit and postsubmit hooks.
type ServerConfig struct {
	Presubmit  []ServerHook `toml:"presubmit"`
	Postsubmit []ServerHook `toml:"postsubmit"`
}

// ClientHook represents a command executed locally in the user's workspace before submit.
type ClientHook struct {
	Name         string `toml:"name"`
	Command      string `toml:"command"`
	FailOnOutput bool   `toml:"fail_on_output"`
	Description  string `toml:"description"`
}

// ServerHook represents a gate executed on niebla-server during SubmitCL.
type ServerHook struct {
	Name           string        `toml:"name"`
	Type           string        `toml:"type"` // "metadata", "tree_scan", "cas_binary", "in_tree", "exec"
	Blocking       *bool         `toml:"blocking"`
	Description    string        `toml:"description"`
	TimeoutSeconds int           `toml:"timeout_seconds"`
	Metadata       *MetadataRule `toml:"metadata"`
	TreeScan       *TreeScanRule `toml:"tree_scan"`
	CasSHA256      string        `toml:"cas_sha256"`
	Args           []string      `toml:"args"`
	Entrypoint     string        `toml:"entrypoint"`
	Command        string        `toml:"command"`
}

// IsBlocking returns true if the hook is blocking (defaults to true if omitted).
func (h *ServerHook) IsBlocking() bool {
	if h.Blocking == nil {
		return true
	}
	return *h.Blocking
}

// MetadataRule defines validation rules on CL metadata fields.
type MetadataRule struct {
	Field     string `toml:"field"` // e.g. "description", "author"
	Pattern   string `toml:"pattern"`
	MinLength int    `toml:"min_length"`
	MaxLength int    `toml:"max_length"`
}

// TreeScanRule defines path and size restrictions across touched files.
type TreeScanRule struct {
	ForbiddenPatterns []string `toml:"forbidden_patterns"`
	MaxFileSizeBytes  uint64   `toml:"max_file_size_bytes"`
}

// CLMetadata contains changelist metadata needed for hook validation.
type CLMetadata struct {
	CLID        int64
	Author      string
	Description string
	ParentCLID  int64
	Reviewers   []string
}

// TouchedFile describes a file modified, added, or deleted in the changelist.
type TouchedFile struct {
	Path      string
	SizeBytes uint64
	BlobSHA   []byte
	Status    string // "added", "modified", "deleted"
}

// HookFailure describes a single policy violation detected by a hook.
type HookFailure struct {
	HookName string `json:"hook_name"`
	Type     string `json:"type"`
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

// EvaluationResult summarizes the outcome of evaluating all presubmit hooks.
type EvaluationResult struct {
	Passed   bool          `json:"passed"`
	Failures []HookFailure `json:"failures,omitempty"`
}

// ParseTOML parses .niebla/hooks.toml content.
func ParseTOML(data []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse hooks TOML: %w", err)
	}
	return &cfg, nil
}

// EvaluateMetadata validates changelist metadata against all metadata hooks.
func EvaluateMetadata(hook ServerHook, meta CLMetadata) []HookFailure {
	if hook.Metadata == nil {
		return nil
	}
	rule := hook.Metadata
	var failures []HookFailure
	blocking := hook.IsBlocking()

	targetVal := meta.Description
	fieldName := "description"
	if strings.ToLower(rule.Field) == "author" {
		targetVal = meta.Author
		fieldName = "author"
	}

	trimmed := strings.TrimSpace(targetVal)

	if rule.MinLength > 0 && len(trimmed) < rule.MinLength {
		failures = append(failures, HookFailure{
			HookName: hook.Name,
			Type:     hook.Type,
			Message:  fmt.Sprintf("%s length (%d) is shorter than minimum required length (%d)", fieldName, len(trimmed), rule.MinLength),
			Blocking: blocking,
		})
	}

	if rule.MaxLength > 0 && len(trimmed) > rule.MaxLength {
		failures = append(failures, HookFailure{
			HookName: hook.Name,
			Type:     hook.Type,
			Message:  fmt.Sprintf("%s length (%d) exceeds maximum allowed length (%d)", fieldName, len(trimmed), rule.MaxLength),
			Blocking: blocking,
		})
	}

	if rule.Pattern != "" {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			failures = append(failures, HookFailure{
				HookName: hook.Name,
				Type:     hook.Type,
				Message:  fmt.Sprintf("invalid regex pattern %q: %v", rule.Pattern, err),
				Blocking: blocking,
			})
		} else if !re.MatchString(trimmed) {
			failures = append(failures, HookFailure{
				HookName: hook.Name,
				Type:     hook.Type,
				Message:  fmt.Sprintf("%s does not match required pattern %q", fieldName, rule.Pattern),
				Blocking: blocking,
			})
		}
	}

	return failures
}

// EvaluateTreeScan validates touched files against path pattern and size restrictions.
func EvaluateTreeScan(hook ServerHook, touched []TouchedFile) []HookFailure {
	if hook.TreeScan == nil {
		return nil
	}
	rule := hook.TreeScan
	var failures []HookFailure
	blocking := hook.IsBlocking()

	for _, file := range touched {
		// Deleted files don't introduce new forbidden paths or binary bloat
		if file.Status == "deleted" {
			continue
		}

		cleanPath := path.Clean(file.Path)

		// Check forbidden patterns
		for _, pattern := range rule.ForbiddenPatterns {
			if matchPathPattern(pattern, cleanPath) {
				failures = append(failures, HookFailure{
					HookName: hook.Name,
					Type:     hook.Type,
					Path:     cleanPath,
					Message:  fmt.Sprintf("path %q matches forbidden pattern %q", cleanPath, pattern),
					Blocking: blocking,
				})
			}
		}

		// Check max file size
		if rule.MaxFileSizeBytes > 0 && file.SizeBytes > rule.MaxFileSizeBytes {
			failures = append(failures, HookFailure{
				HookName: hook.Name,
				Type:     hook.Type,
				Path:     cleanPath,
				Message:  fmt.Sprintf("file size (%d bytes) exceeds maximum permitted size (%d bytes)", file.SizeBytes, rule.MaxFileSizeBytes),
				Blocking: blocking,
			})
		}
	}

	return failures
}

// EvaluateServerPresubmit evaluates all configured server-side presubmit hooks.
func EvaluateServerPresubmit(hooks []ServerHook, meta CLMetadata, touched []TouchedFile) EvaluationResult {
	result := EvaluationResult{Passed: true}

	for _, hook := range hooks {
		switch hook.Type {
		case "metadata":
			fails := EvaluateMetadata(hook, meta)
			result.Failures = append(result.Failures, fails...)
		case "tree_scan":
			fails := EvaluateTreeScan(hook, touched)
			result.Failures = append(result.Failures, fails...)
		}
	}

	for _, f := range result.Failures {
		if f.Blocking {
			result.Passed = false
			break
		}
	}

	return result
}

// matchPathPattern matches a pattern against a slash-separated file path.
// If pattern contains no slash, it matches against the basename or any component.
// If pattern contains a slash, it matches against the relative path.
func matchPathPattern(pattern, filePath string) bool {
	pattern = path.Clean(pattern)
	filePath = path.Clean(filePath)

	// Direct match
	if matched, _ := path.Match(pattern, filePath); matched {
		return true
	}

	// Basename match if pattern has no slashes (e.g. "*.pem" or "id_rsa*")
	if !strings.Contains(pattern, "/") {
		base := path.Base(filePath)
		if matched, _ := path.Match(pattern, base); matched {
			return true
		}
		if matched, _ := filepath.Match(pattern, base); matched {
			return true
		}
	}

	// Directory prefix match (e.g. "secrets/*")
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		if strings.HasPrefix(filePath, prefix+"/") {
			return true
		}
	}

	return false
}
