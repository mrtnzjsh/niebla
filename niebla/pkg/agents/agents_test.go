package agents

import (
	"testing"
)

func TestParseTOML(t *testing.T) {
	rawTOML := `
[[agent]]
name = "sec-scanner"
display_name = "Security Pattern Scanner"
type = "in_tree"
entrypoint = "tools/agents/sec-scanner/scan.sh"
trigger_paths = ["pkg/security/**", "**/auth/**"]
auto_approve = true
capture_logs = true
post_inline_diagnostics = true
timeout_seconds = 60

[[agent]]
name = "package-builder"
display_name = "Package Build Runner"
type = "cas_binary"
cas_sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
args = ["--strict"]
blocking = false
`

	cfg, err := ParseTOML([]byte(rawTOML))
	if err != nil {
		t.Fatalf("failed to parse TOML: %v", err)
	}

	if len(cfg.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(cfg.Agents))
	}

	// 1. First agent: blocking defaults to true
	a1 := cfg.Agents[0]
	if a1.Name != "sec-scanner" || !a1.IsBlocking() || !a1.AutoApprove || !a1.CaptureLogs || !a1.PostInlineDiagnostics {
		t.Errorf("unexpected agent 1 config: %+v", a1)
	}
	if a1.TimeoutSeconds != 60 {
		t.Errorf("expected timeout 60s, got %d", a1.TimeoutSeconds)
	}

	// 2. Second agent: explicit blocking = false
	a2 := cfg.Agents[1]
	if a2.Name != "package-builder" || a2.IsBlocking() || a2.CasSHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("unexpected agent 2 config: %+v", a2)
	}
}

func TestParseTOML_Invalid(t *testing.T) {
	_, err := ParseTOML([]byte("invalid [toml = agents"))
	if err == nil {
		t.Errorf("expected error for invalid TOML syntax")
	}
}

func TestMatchAgents(t *testing.T) {
	agents := []AgentDef{
		{
			Name:         "sec-bot",
			TriggerPaths: []string{"pkg/security/**", "**/auth/**"},
		},
		{
			Name:         "go-linter",
			TriggerPaths: []string{"**/*.go"},
		},
		{
			Name:         "catch-all",
			TriggerPaths: nil, // runs on all changes
		},
	}

	// Case 1: Touched file is security auth Go file -> matches all 3
	m1 := MatchAgents(agents, []string{"pkg/security/auth/token.go"})
	if len(m1) != 3 {
		t.Errorf("expected 3 agents matched, got %d", len(m1))
	}

	// Case 2: Touched file is regular Go file -> matches go-linter and catch-all
	m2 := MatchAgents(agents, []string{"cmd/app/main.go"})
	if len(m2) != 2 {
		t.Errorf("expected 2 agents matched, got %d", len(m2))
	}

	// Case 3: Touched file is docs only -> matches only catch-all
	m3 := MatchAgents(agents, []string{"docs/readme.md"})
	if len(m3) != 1 || m3[0].Name != "catch-all" {
		t.Errorf("expected only catch-all, got %+v", m3)
	}
}

func TestParseDiagnostics(t *testing.T) {
	compilerOutput := `
# Output header from build
pkg/auth/token.go:42:15: undefined: ValidateExpiration
src/main.c:10:5: error: expected ';' before 'return'
src/utils.c:88: warning: comparison between signed and unsigned integer
app.py:12:1: E302 expected 2 blank lines, found 1
some random log line that does not match
`

	diagnostics := ParseDiagnostics(compilerOutput)
	if len(diagnostics) != 4 {
		t.Fatalf("expected 4 extracted diagnostics, got %d", len(diagnostics))
	}

	// 1. Go compiler error
	d1 := diagnostics[0]
	if d1.Path != "pkg/auth/token.go" || d1.LineStart != 42 || d1.Column != 15 || d1.Severity != "error" || d1.Message != "undefined: ValidateExpiration" {
		t.Errorf("unexpected d1: %+v", d1)
	}

	// 2. Clang error with explicit severity
	d2 := diagnostics[1]
	if d2.Path != "src/main.c" || d2.LineStart != 10 || d2.Column != 5 || d2.Severity != "error" || d2.Message != "expected ';' before 'return'" {
		t.Errorf("unexpected d2: %+v", d2)
	}

	// 3. Warning
	d3 := diagnostics[2]
	if d3.Path != "src/utils.c" || d3.LineStart != 88 || d3.Severity != "warning" {
		t.Errorf("unexpected d3: %+v", d3)
	}

	// 4. Linter message
	d4 := diagnostics[3]
	if d4.Path != "app.py" || d4.LineStart != 12 || d4.Column != 1 {
		t.Errorf("unexpected d4: %+v", d4)
	}
}

func TestMatchAgentsWithExplicitAndOptIn(t *testing.T) {
	agents := []AgentDef{
		{
			Name:         "a11y-bot",
			Type:         "ai",
			PromptFile:   ".niebla/prompts/accessibility.md",
			TriggerPaths: []string{"internal/web/views/**/*.templ"},
		},
		{
			Name:  "fips-audit-bot",
			Type:  "ai",
			OptIn: true, // Only runs when explicitly referenced by OWNERS.toml
		},
	}

	// 1. Touching a backend file without explicit OWNERS.toml agents -> 0 matched
	m1 := MatchAgentsWithExplicit(agents, []string{"pkg/cas/storage.go"}, nil)
	if len(m1) != 0 {
		t.Fatalf("expected 0 matched agents, got %+v", m1)
	}

	// 2. Touching a backend file where OWNERS.toml explicitly requests fips-audit-bot -> 1 matched
	m2 := MatchAgentsWithExplicit(agents, []string{"pkg/auth/crypto.go"}, []string{"fips-audit-bot"})
	if len(m2) != 1 || m2[0].Name != "fips-audit-bot" {
		t.Fatalf("expected fips-audit-bot matched via explicit OWNERS list, got %+v", m2)
	}
}

func TestParseReviewOutput_StructuredJSONReport(t *testing.T) {
	jsonOutput := "```json\n" + `{
  "verdict": "changes_requested",
  "summary": "Found 1 accessibility issue in inline comment form.",
  "comments": [
    {
      "path": "internal/web/views/comment_box.templ",
      "line_start": 172,
      "line_end": 175,
      "severity": "warning",
      "message": "Input element should include an explicit aria-label for screen readers.",
      "suggestion_code": "<input type=\"text\" name=\"author\" aria-label=\"Comment Author\" />"
    }
  ]
}` + "\n```"

	report, diags := ParseReviewOutput(jsonOutput)
	if report == nil {
		t.Fatalf("expected non-nil AgentReviewReport")
	}
	if report.Verdict != "changes_requested" {
		t.Errorf("expected verdict changes_requested, got %q", report.Verdict)
	}
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic comment, got %d", len(diags))
	}
	d := diags[0]
	if d.Path != "internal/web/views/comment_box.templ" || d.LineStart != 172 || d.LineEnd != 175 {
		t.Errorf("unexpected diagnostic location: %+v", d)
	}
	if d.SuggestionStartLine != 172 || d.SuggestionEndLine != 175 {
		t.Errorf("expected suggestion range 172-175, got %d-%d", d.SuggestionStartLine, d.SuggestionEndLine)
	}
	if d.SuggestionCode == "" {
		t.Errorf("expected non-empty SuggestionCode")
	}
}
