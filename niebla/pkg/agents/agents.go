package agents

import (
	"bufio"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Config represents the top-level schema of .niebla/agents.toml.
type Config struct {
	Agents []AgentDef `toml:"agent"`
}

// AgentDef defines a specialized automated review persona.
type AgentDef struct {
	Name                  string   `toml:"name"`
	DisplayName           string   `toml:"display_name"`
	Type                  string   `toml:"type"` // "in_tree", "cas_binary", "exec"
	Entrypoint            string   `toml:"entrypoint"`
	CasSHA256             string   `toml:"cas_sha256"`
	Args                  []string `toml:"args"`
	Command               string   `toml:"command"`
	TriggerPaths          []string `toml:"trigger_paths"`
	Blocking              *bool    `toml:"blocking"`
	AutoApprove           bool     `toml:"auto_approve"`
	CaptureLogs           bool     `toml:"capture_logs"`
	PostInlineDiagnostics bool     `toml:"post_inline_diagnostics"`
	TimeoutSeconds        int      `toml:"timeout_seconds"`
}

// IsBlocking returns true if the agent check is blocking (defaults to true).
func (a *AgentDef) IsBlocking() bool {
	if a.Blocking == nil {
		return true
	}
	return *a.Blocking
}

// DiagnosticComment represents an inline code diagnostic extracted from tool output.
type DiagnosticComment struct {
	Path      string `json:"path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Column    int    `json:"column,omitempty"`
	Severity  string `json:"severity"` // "error", "warning", "info"
	Message   string `json:"message"`
}

// ParseTOML parses the .niebla/agents.toml configuration file.
func ParseTOML(data []byte) (*Config, error) {
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse agents TOML: %w", err)
	}
	return &cfg, nil
}

// MatchAgents filters agents based on touched file paths in a changelist.
func MatchAgents(agents []AgentDef, touchedFiles []string) []AgentDef {
	var matched []AgentDef
	for _, a := range agents {
		if len(a.TriggerPaths) == 0 {
			matched = append(matched, a)
			continue
		}
		if a.MatchesAnyFile(touchedFiles) {
			matched = append(matched, a)
		}
	}
	return matched
}

// MatchesAnyFile checks if any of the touched files match the agent's trigger paths.
func (a *AgentDef) MatchesAnyFile(touchedFiles []string) bool {
	for _, f := range touchedFiles {
		for _, pattern := range a.TriggerPaths {
			if matchPathPattern(pattern, f) {
				return true
			}
		}
	}
	return false
}

// matchPathPattern evaluates glob patterns against paths.
func matchPathPattern(pattern, filePath string) bool {
	cleanPath := path.Clean(filePath)
	cleanPattern := path.Clean(pattern)

	// Direct match
	if matched, _ := path.Match(cleanPattern, cleanPath); matched {
		return true
	}

	// Handle "**/" prefix/infix
	if strings.HasPrefix(cleanPattern, "**/") {
		subPattern := strings.TrimPrefix(cleanPattern, "**/")
		base := path.Base(cleanPath)
		if matched, _ := path.Match(subPattern, base); matched {
			return true
		}
		if matched, _ := path.Match(subPattern, cleanPath); matched {
			return true
		}
	}

	// Basename match if pattern contains no slashes (e.g. "*.go")
	if !strings.Contains(cleanPattern, "/") {
		base := path.Base(cleanPath)
		if matched, _ := path.Match(cleanPattern, base); matched {
			return true
		}
		if matched, _ := filepath.Match(cleanPattern, base); matched {
			return true
		}
	}

	// Directory prefix match (e.g. "pkg/security/**" or "pkg/security/*")
	prefix := cleanPattern
	if idx := strings.Index(cleanPattern, "/*"); idx != -1 {
		prefix = cleanPattern[:idx]
	}
	if strings.HasPrefix(cleanPath, prefix+"/") || cleanPath == prefix {
		return true
	}

	return false
}

// Regular expression matching compiler/linter error output:
// Examples:
//
//	pkg/auth/token.go:42:15: undefined: ValidateExpiration
//	src/main.c:10:5: error: expected ';' before 'return'
//	app.py:12:1: E302 expected 2 blank lines
//	server.go:15: syntax error
var diagnosticRegex = regexp.MustCompile(`^([^:\s\t]+):(\d+)(?::(\d+))?:\s*(?:(error|warning|info|note):\s*)?(.+)$`)

// ParseDiagnostics parses compiler and linter output into structured diagnostic comments.
func ParseDiagnostics(output string) []DiagnosticComment {
	var diagnostics []DiagnosticComment
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		matches := diagnosticRegex.FindStringSubmatch(line)
		if len(matches) < 6 {
			continue
		}

		filePath := path.Clean(matches[1])
		lineNum, err := strconv.Atoi(matches[2])
		if err != nil || lineNum <= 0 {
			continue
		}

		col := 0
		if matches[3] != "" {
			col, _ = strconv.Atoi(matches[3])
		}

		severity := strings.ToLower(matches[4])
		if severity == "" {
			severity = "error"
		}

		msg := strings.TrimSpace(matches[5])

		diagnostics = append(diagnostics, DiagnosticComment{
			Path:      filePath,
			LineStart: lineNum,
			LineEnd:   lineNum,
			Column:    col,
			Severity:  severity,
			Message:   msg,
		})
	}

	return diagnostics
}
