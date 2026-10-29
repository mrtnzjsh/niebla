package config

import (
	"testing"
)

func TestMatchAgentAuthor(t *testing.T) {
	patterns := DefaultAgentAuthorPatterns

	tests := []struct {
		author   string
		expected bool
	}{
		{"agent/refactor-worker", true},
		{"AGENT/linter", true},
		{"bot/ci-runner", true},
		{"dependabot[bot]", true},
		{"reviewer@agent.corp", true},
		{"system", true},
		{"SYSTEM", true},
		{"ci/test-matrix", true},
		{"alice", false},
		{"bob@corp.com", false},
		{"human-dev", false},
		{"", false},
		{"   ", false},
	}

	for _, tt := range tests {
		got := MatchAgentAuthor(tt.author, patterns)
		if got != tt.expected {
			t.Errorf("MatchAgentAuthor(%q) = %v; want %v", tt.author, got, tt.expected)
		}
	}
}

func TestCritiqueConfig_IsAgentAuthor(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Critique.IsAgentAuthor("agent/code-fixer") {
		t.Errorf("expected agent/code-fixer to be recognized as agent")
	}
	if cfg.Critique.IsAgentAuthor("developer@corp") {
		t.Errorf("expected developer@corp to NOT be recognized as agent")
	}

	// Custom patterns
	cfg.Critique.AgentAuthorPatterns = []string{"custom-bot-*", "ai-*"}
	if !cfg.Critique.IsAgentAuthor("custom-bot-123") {
		t.Errorf("expected custom-bot-123 to match custom pattern")
	}
	if !cfg.Critique.IsAgentAuthor("ai-assistant") {
		t.Errorf("expected ai-assistant to match custom pattern")
	}
	if cfg.Critique.IsAgentAuthor("agent/old") {
		t.Errorf("expected agent/old to NOT match when overridden")
	}
}
