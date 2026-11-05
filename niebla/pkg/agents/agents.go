package agents

import (
	"bufio"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"niebla/pkg/ignore"

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
	Type                  string   `toml:"type"` // "ai", "in_tree", "cas_binary", "exec"
	Entrypoint            string   `toml:"entrypoint"`
	CasSHA256             string   `toml:"cas_sha256"`
	PromptFile            string   `toml:"prompt_file"`
	Args                  []string `toml:"args"`
	Command               string   `toml:"command"`
	TriggerPaths          []string `toml:"trigger_paths"`
	OptIn                 bool     `toml:"opt_in"`
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
	Path                string `json:"path"`
	LineStart           int    `json:"line_start"`
	LineEnd             int    `json:"line_end"`
	Column              int    `json:"column,omitempty"`
	Severity            string `json:"severity"` // "error", "warning", "info"
	Message             string `json:"message"`
	SuggestionStartLine int    `json:"suggestion_start_line,omitempty"`
	SuggestionEndLine   int    `json:"suggestion_end_line,omitempty"`
	SuggestionCode      string `json:"suggestion_code,omitempty"`
}

// AgentReviewComment represents a structured inline comment (with optional code suggestion)
// produced by an AI or static analysis review agent.
type AgentReviewComment struct {
	Path                string `json:"path"`
	LineStart           int    `json:"line_start"`
	LineEnd             int    `json:"line_end,omitempty"`
	Column              int    `json:"column,omitempty"`
	Severity            string `json:"severity,omitempty"` // "error", "warning", "info"
	Message             string `json:"message"`
	SuggestionStartLine int    `json:"suggestion_start_line,omitempty"`
	SuggestionEndLine   int    `json:"suggestion_end_line,omitempty"`
	SuggestionCode      string `json:"suggestion_code,omitempty"`
}

// AgentReviewReport represents the structured JSON output contract for review agents.
type AgentReviewReport struct {
	Verdict  string               `json:"verdict,omitempty"` // "lgtm", "approved", "changes_requested", "comment"
	Summary  string               `json:"summary,omitempty"`
	Comments []AgentReviewComment `json:"comments,omitempty"`
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
	return MatchAgentsWithExplicit(agents, touchedFiles, nil)
}

// MatchAgentsWithExplicit filters agents based on touched file paths OR explicit agent names
// requested by subtree OWNERS.toml policies.
func MatchAgentsWithExplicit(agents []AgentDef, touchedFiles []string, explicitNames []string) []AgentDef {
	explicitMap := make(map[string]bool, len(explicitNames))
	for _, name := range explicitNames {
		trimmed := strings.ToLower(strings.TrimSpace(name))
		if trimmed != "" {
			explicitMap[trimmed] = true
		}
	}

	var matched []AgentDef
	for _, agent := range agents {
		if explicitMap[strings.ToLower(strings.TrimSpace(agent.Name))] {
			matched = append(matched, agent)
			continue
		}
		if agent.OptIn {
			continue
		}
		if len(agent.TriggerPaths) == 0 {
			matched = append(matched, agent)
			continue
		}
		if agent.MatchesAnyFile(touchedFiles) {
			matched = append(matched, agent)
		}
	}
	return matched
}

// MatchesAnyFile checks if any of the touched files match the agent's trigger paths.
func (a *AgentDef) MatchesAnyFile(touchedFiles []string) bool {
	for _, file := range touchedFiles {
		for _, pattern := range a.TriggerPaths {
			if ignore.MatchPathPattern(pattern, file) {
				return true
			}
		}
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

// ParseReviewOutput parses either a structured JSON AgentReviewReport (emitting multi-line
// comments and one-click code suggestions) or standard compiler/linter diagnostic lines.
func ParseReviewOutput(output string) (*AgentReviewReport, []DiagnosticComment) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, nil
	}

	if jsonBlob := extractJSONObject(trimmed); jsonBlob != "" {
		var report AgentReviewReport
		if err := json.Unmarshal([]byte(jsonBlob), &report); err == nil {
			if report.Verdict != "" || report.Summary != "" || len(report.Comments) > 0 {
				var diags []DiagnosticComment
				for i := range report.Comments {
					comment := &report.Comments[i]
					comment.Path = path.Clean(strings.TrimSpace(comment.Path))
					if comment.LineStart <= 0 {
						comment.LineStart = 1
					}
					if comment.LineEnd < comment.LineStart {
						comment.LineEnd = comment.LineStart
					}
					if comment.Severity == "" {
						comment.Severity = "warning"
					}
					if comment.SuggestionCode != "" {
						if comment.SuggestionStartLine <= 0 {
							comment.SuggestionStartLine = comment.LineStart
						}
						if comment.SuggestionEndLine < comment.SuggestionStartLine {
							comment.SuggestionEndLine = comment.LineEnd
						}
					}
					diags = append(diags, DiagnosticComment{
						Path:                comment.Path,
						LineStart:           comment.LineStart,
						LineEnd:             comment.LineEnd,
						Column:              comment.Column,
						Severity:            comment.Severity,
						Message:             comment.Message,
						SuggestionStartLine: comment.SuggestionStartLine,
						SuggestionEndLine:   comment.SuggestionEndLine,
						SuggestionCode:      comment.SuggestionCode,
					})
				}
				return &report, diags
			}
		}
	}

	return nil, ParseDiagnostics(output)
}

func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start == -1 || end == -1 || end <= start {
		return ""
	}
	return s[start : end+1]
}
