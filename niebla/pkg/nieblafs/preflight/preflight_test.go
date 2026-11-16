package preflight

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckFuseConf(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("Valid with user_allow_other", func(t *testing.T) {
		confPath := filepath.Join(tempDir, "fuse.conf")
		content := "# Comment\n\nuser_allow_other\n"
		if err := os.WriteFile(confPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		res := CheckFuseConf(confPath)
		if res.Status != StatusPass {
			t.Errorf("expected StatusPass, got %s: %s", res.Status, res.Message)
		}
	})

	t.Run("Commented out user_allow_other", func(t *testing.T) {
		confPath := filepath.Join(tempDir, "fuse_commented.conf")
		content := "# user_allow_other\n; user_allow_other\n"
		if err := os.WriteFile(confPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}

		res := CheckFuseConf(confPath)
		if res.Status != StatusWarn {
			t.Errorf("expected StatusWarn, got %s: %s", res.Status, res.Message)
		}
	})

	t.Run("Missing fuse.conf", func(t *testing.T) {
		res := CheckFuseConf(filepath.Join(tempDir, "nonexistent.conf"))
		if res.Status != StatusWarn {
			t.Errorf("expected StatusWarn for missing config, got %s", res.Status)
		}
	})
}

func TestCheckFusermount(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("Non-existent binary", func(t *testing.T) {
		res := CheckFusermount(filepath.Join(tempDir, "does-not-exist"))
		// If fallback on host exists it might find host binary, so pass dummy that fails all candidates
		if res.Name == "" {
			t.Errorf("expected CheckItem to have Name set")
		}
	})

	t.Run("Unprivileged mock binary without setuid", func(t *testing.T) {
		mockBin := filepath.Join(tempDir, "mock-fusermount")
		if err := os.WriteFile(mockBin, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
			t.Fatalf("failed to create mock binary: %v", err)
		}

		res := CheckFusermount(mockBin)
		if res.Status != StatusWarn {
			t.Errorf("expected StatusWarn for non-setuid binary, got %s (%s)", res.Status, res.Message)
		}
		if !strings.Contains(res.Message, "missing setuid") {
			t.Errorf("expected message to mention missing setuid, got: %s", res.Message)
		}
	})
}

func TestCheckDevFuse(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("Missing device node", func(t *testing.T) {
		res := CheckDevFuse(filepath.Join(tempDir, "fuse-dev"))
		if res.Status != StatusFail {
			t.Errorf("expected StatusFail for missing device, got %s", res.Status)
		}
	})

	t.Run("Regular file instead of char device", func(t *testing.T) {
		fakeDev := filepath.Join(tempDir, "fake-fuse")
		if err := os.WriteFile(fakeDev, []byte(""), 0666); err != nil {
			t.Fatalf("failed to create dummy file: %v", err)
		}

		res := CheckDevFuse(fakeDev)
		if res.Status != StatusWarn {
			t.Errorf("expected StatusWarn for regular file, got %s", res.Status)
		}
	})
}

func TestVerifyFUSEReadinessAndDiagnostics(t *testing.T) {
	report := VerifyFUSEReadiness()
	if len(report.Checks) != 3 {
		t.Errorf("expected 3 checks, got %d", len(report.Checks))
	}

	diag := FormatDiagnostics(report)
	if len(diag) == 0 {
		t.Errorf("expected non-empty diagnostics output")
	}
}
