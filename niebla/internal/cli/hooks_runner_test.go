package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHooksRunner_PostCreate(t *testing.T) {
	tempDir := t.TempDir()
	nieblaDir := filepath.Join(tempDir, ".niebla")
	if err := os.MkdirAll(nieblaDir, 0755); err != nil {
		t.Fatalf("failed to create .niebla dir: %v", err)
	}

	outputMarker := filepath.Join(tempDir, "post_create_marker.txt")
	hooksTOML := `
[[client.post_create]]
name = "write-marker"
command = "echo workspace=$NIEBLA_WORKSPACE_NAME base_cl=$NIEBLA_BASE_CL > post_create_marker.txt"
fail_on_error = true
`
	if err := os.WriteFile(filepath.Join(nieblaDir, "hooks.toml"), []byte(hooksTOML), 0644); err != nil {
		t.Fatalf("failed to write hooks.toml: %v", err)
	}

	ctx := context.Background()
	err := runPostCreateHooks(ctx, tempDir, "test-ws", 42, "127.0.0.1:50051")
	if err != nil {
		t.Fatalf("runPostCreateHooks failed: %v", err)
	}

	content, err := os.ReadFile(outputMarker)
	if err != nil {
		t.Fatalf("failed to read marker file: %v", err)
	}

	expected := "workspace=test-ws base_cl=42\n"
	if string(content) != expected {
		t.Errorf("expected marker content %q, got %q", expected, string(content))
	}
}

func TestHooksRunner_PostCreate_FailOnErrorFalse(t *testing.T) {
	tempDir := t.TempDir()
	nieblaDir := filepath.Join(tempDir, ".niebla")
	if err := os.MkdirAll(nieblaDir, 0755); err != nil {
		t.Fatalf("failed to create .niebla dir: %v", err)
	}

	hooksTOML := `
[[client.post_create]]
name = "non-fatal-failing-hook"
command = "exit 1"
fail_on_error = false
`
	if err := os.WriteFile(filepath.Join(nieblaDir, "hooks.toml"), []byte(hooksTOML), 0644); err != nil {
		t.Fatalf("failed to write hooks.toml: %v", err)
	}

	ctx := context.Background()
	err := runPostCreateHooks(ctx, tempDir, "test-ws", 10, "127.0.0.1:50051")
	if err != nil {
		t.Errorf("expected post_create hook to not fail when fail_on_error=false, got: %v", err)
	}
}

func TestHooksRunner_PostCreate_FailOnErrorTrue(t *testing.T) {
	tempDir := t.TempDir()
	nieblaDir := filepath.Join(tempDir, ".niebla")
	if err := os.MkdirAll(nieblaDir, 0755); err != nil {
		t.Fatalf("failed to create .niebla dir: %v", err)
	}

	hooksTOML := `
[[client.post_create]]
name = "fatal-failing-hook"
command = "exit 1"
fail_on_error = true
`
	if err := os.WriteFile(filepath.Join(nieblaDir, "hooks.toml"), []byte(hooksTOML), 0644); err != nil {
		t.Fatalf("failed to write hooks.toml: %v", err)
	}

	ctx := context.Background()
	err := runPostCreateHooks(ctx, tempDir, "test-ws", 10, "127.0.0.1:50051")
	if err == nil {
		t.Errorf("expected post_create hook to fail when fail_on_error=true")
	}
}

func TestHooksRunner_PostSync(t *testing.T) {
	tempDir := t.TempDir()
	nieblaDir := filepath.Join(tempDir, ".niebla")
	if err := os.MkdirAll(nieblaDir, 0755); err != nil {
		t.Fatalf("failed to create .niebla dir: %v", err)
	}

	outputMarker := filepath.Join(tempDir, "post_sync_marker.txt")
	hooksTOML := `
[[client.post_sync]]
name = "write-sync-marker"
command = "echo cl_id=$NIEBLA_CL_ID server=$NIEBLA_SERVER > post_sync_marker.txt"
fail_on_error = true
`
	if err := os.WriteFile(filepath.Join(nieblaDir, "hooks.toml"), []byte(hooksTOML), 0644); err != nil {
		t.Fatalf("failed to write hooks.toml: %v", err)
	}

	ctx := context.Background()
	err := runPostSyncHooks(ctx, tempDir, "test-ws", 999, "server.internal:50051")
	if err != nil {
		t.Fatalf("runPostSyncHooks failed: %v", err)
	}

	content, err := os.ReadFile(outputMarker)
	if err != nil {
		t.Fatalf("failed to read marker file: %v", err)
	}

	expected := "cl_id=999 server=server.internal:50051\n"
	if string(content) != expected {
		t.Errorf("expected marker content %q, got %q", expected, string(content))
	}
}

func TestHooksRunner_NoHooksFile(t *testing.T) {
	tempDir := t.TempDir()
	ctx := context.Background()

	if err := runPostCreateHooks(ctx, tempDir, "ws", 1, "srv"); err != nil {
		t.Errorf("expected nil error on missing hooks file, got: %v", err)
	}
	if err := runPostSyncHooks(ctx, tempDir, "ws", 1, "srv"); err != nil {
		t.Errorf("expected nil error on missing hooks file, got: %v", err)
	}
	if err := runClientPresubmitHooks(ctx, tempDir); err != nil {
		t.Errorf("expected nil error on missing hooks file, got: %v", err)
	}
}

