package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"niebla/internal/cli/config"
)

func TestSubmitHelp(t *testing.T) {
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"submit", "--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected submit --help to succeed: %v", err)
	}

	out := buf.String()
	for _, flag := range []string{"--cl", "--force", "--no-verify"} {
		if !strings.Contains(out, flag) {
			t.Errorf("expected flag %q in submit help output", flag)
		}
	}
}

func TestSubmitClientPresubmitHook_BlockedAndBypassed(t *testing.T) {
	tempDir := t.TempDir()
	cfgDir := filepath.Join(tempDir, "config")
	wsMount := filepath.Join(tempDir, "ws-mount")
	_ = os.MkdirAll(wsMount, 0755)

	// Create workspace configuration with active CL = 42
	mgr, err := config.NewManager(cfgDir)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}
	wsConfig := &config.WorkspaceConfig{
		Name:     "test-ws",
		ActiveCL: 42,
		BaseCL:   1,
		MountDir: wsMount,
	}
	if err := mgr.Save(wsConfig); err != nil {
		t.Fatalf("failed to save wsConfig: %v", err)
	}

	// 1. Write .niebla/hooks.toml with a failing client hook
	hooksDir := filepath.Join(wsMount, ".niebla")
	_ = os.MkdirAll(hooksDir, 0755)
	hooksContent := `
[[client.presubmit]]
name = "lint-check"
command = "echo 'formatting error found' && exit 1"
description = "Sample failing linter"
`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.toml"), []byte(hooksContent), 0644); err != nil {
		t.Fatalf("failed to write hooks.toml: %v", err)
	}

	// 2. Running submit should be blocked by client presubmit hook
	rootCmd := NewRootCmd()
	rootCmd.SetArgs([]string{
		"submit",
		"--workspace", "test-ws",
		"--config-dir", cfgDir,
	})

	err = rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected submit to be blocked by client presubmit hook, but got success")
	}
	if !strings.Contains(err.Error(), "client presubmit hook \"lint-check\" failed") {
		t.Fatalf("unexpected error message: %v", err)
	}

	// 3. Running submit with --no-verify bypasses the client hook
	// It will attempt to dial the mock/missing server and fail at network level, confirming the hook was bypassed!
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	rootCmd = NewRootCmd()
	rootCmd.SetContext(ctx)
	rootCmd.SetArgs([]string{
		"submit",
		"--workspace", "test-ws",
		"--config-dir", cfgDir,
		"--server", "127.0.0.1:59999", // non-listening port
		"--no-verify",
	})

	err = rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected network failure after bypassing hook, got success")
	}
	if strings.Contains(err.Error(), "client presubmit hook") {
		t.Fatalf("client hook was NOT bypassed despite --no-verify: %v", err)
	}

	// 4. Test fail_on_output = true
	failOnOutputHooks := `
[[client.presubmit]]
name = "clean-output-check"
command = "echo 'unexpected warnings generated'"
fail_on_output = true
`
	if err := os.WriteFile(filepath.Join(hooksDir, "hooks.toml"), []byte(failOnOutputHooks), 0644); err != nil {
		t.Fatalf("failed to write hooks.toml: %v", err)
	}

	rootCmd = NewRootCmd()
	rootCmd.SetArgs([]string{
		"submit",
		"--workspace", "test-ws",
		"--config-dir", cfgDir,
	})

	err = rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected hook to fail due to fail_on_output=true")
	}
	if !strings.Contains(err.Error(), "fail_on_output=true") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
