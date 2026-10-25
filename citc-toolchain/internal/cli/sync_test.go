package cli

import (
	"bytes"
	"testing"
)

func TestNewSyncCmd_Flags(t *testing.T) {
	flags := &RootFlags{}
	cmd := NewSyncCmd(flags)

	if cmd.Use != "sync" {
		t.Errorf("expected command use 'sync', got %q", cmd.Use)
	}

	dryRunFlag := cmd.Flag("dry-run")
	if dryRunFlag == nil {
		t.Fatalf("expected --dry-run flag on sync command")
	}
	if dryRunFlag.DefValue != "false" {
		t.Errorf("expected --dry-run default false, got %s", dryRunFlag.DefValue)
	}
}

func TestNewSyncCmd_MissingWorkspace(t *testing.T) {
	flags := &RootFlags{
		ConfigDir: t.TempDir(),
		Workspace: "non-existent-ws",
	}
	cmd := NewSyncCmd(flags)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when syncing non-existent workspace, got nil")
	}
}
