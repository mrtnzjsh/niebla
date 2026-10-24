package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHubAndSpokes(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "citc-skills-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mockHome := filepath.Join(tempDir, "home")
	hubDir := filepath.Join(tempDir, "hub")

	// 1. EnsureHub populates files
	ensuredHub, err := EnsureHub(hubDir)
	if err != nil {
		t.Fatalf("EnsureHub failed: %v", err)
	}
	skillFile := filepath.Join(ensuredHub, "SKILL.md")
	content, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatalf("failed to read generated hub SKILL.md: %v", err)
	}
	if !strings.Contains(string(content), "CitC Version Control System") {
		t.Errorf("SKILL.md missing expected header content: %s", string(content))
	}

	// 2. CheckSpokes initially should show NOT_INSTALLED
	statuses, err := CheckSpokes(hubDir, mockHome)
	if err != nil {
		t.Fatalf("CheckSpokes failed: %v", err)
	}
	for _, s := range statuses {
		if s.Status != "NOT_INSTALLED" {
			t.Errorf("expected NOT_INSTALLED for %s, got %s", s.Name, s.Status)
		}
	}

	// 3. Proactive SyncSpokes: should link all harnesses
	syncStatuses, err := SyncSpokes(hubDir, true, mockHome)
	if err != nil {
		t.Fatalf("SyncSpokes(proactive=true) failed: %v", err)
	}
	for _, s := range syncStatuses {
		if s.Status != "LINKED" {
			t.Errorf("expected LINKED for %s, got %s (%s)", s.Name, s.Status, s.Message)
		}
	}

	// 4. Verify CheckSpokes confirms LINKED
	checkStatuses, err := CheckSpokes(hubDir, mockHome)
	if err != nil {
		t.Fatalf("CheckSpokes after sync failed: %v", err)
	}
	for _, s := range checkStatuses {
		if s.Status != "LINKED" {
			t.Errorf("expected LINKED for %s, got %s", s.Name, s.Status)
		}
	}

	// 5. Test Newly Installed Harness detection (Opportunistic non-proactive mode)
	freshHome := filepath.Join(tempDir, "fresh_home")
	// Simulate user only having .claude
	claudeRoot := filepath.Join(freshHome, ".claude")
	_ = os.MkdirAll(claudeRoot, 0755)

	oppStatuses, err := SyncSpokes(hubDir, false, freshHome)
	if err != nil {
		t.Fatalf("opportunistic sync failed: %v", err)
	}

	for _, s := range oppStatuses {
		if s.Name == "Claude Code" {
			if s.Status != "LINKED" {
				t.Errorf("expected Claude Code to be LINKED, got %s", s.Status)
			}
		} else {
			if s.Status != "SKIPPED" {
				t.Errorf("expected uninstalled harness %s to be SKIPPED, got %s", s.Name, s.Status)
			}
		}
	}

	// Now simulate user later installing Antigravity (~/.gemini appears)
	geminiRoot := filepath.Join(freshHome, ".gemini")
	_ = os.MkdirAll(geminiRoot, 0755)

	// Next opportunistic sync should pick it up!
	oppStatuses2, err := SyncSpokes(hubDir, false, freshHome)
	if err != nil {
		t.Fatalf("second opportunistic sync failed: %v", err)
	}
	for _, s := range oppStatuses2 {
		if s.Name == "Antigravity / Gemini" {
			if s.Status != "LINKED" {
				t.Errorf("expected newly installed Antigravity to be LINKED, got %s", s.Status)
			}
		}
	}
}
