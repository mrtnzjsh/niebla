package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"niebla/internal/cli/config"
)

func TestWorkspaceMountUnmountCmd(t *testing.T) {
	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "workspaces")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	mgr, err := config.NewManager(configDir)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	mountDir := filepath.Join(tempDir, "mount")
	scratchDir := filepath.Join(tempDir, "scratch")
	_ = os.MkdirAll(mountDir, 0755)
	_ = os.MkdirAll(scratchDir, 0755)

	wsConfig := &config.WorkspaceConfig{
		WorkspaceID:  "ws-test-1",
		Name:         "test-ws",
		BaseCL:       1,
		RootTreeHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		MountDir:     mountDir,
		ScratchDir:   scratchDir,
		PID:          0,
		NoFuse:       false,
	}
	if err := mgr.Save(wsConfig); err != nil {
		t.Fatalf("failed to save ws config: %v", err)
	}

	rootFlags := &RootFlags{
		ConfigDir: configDir,
	}

	// 1. Test unmount on inactive workspace (should succeed and set PID=0)
	unmountCmd := newWorkspaceUnmountCmd(rootFlags)
	buf := new(bytes.Buffer)
	unmountCmd.SetOut(buf)
	unmountCmd.SetErr(buf)
	unmountCmd.SetArgs([]string{"test-ws"})

	if err := unmountCmd.Execute(); err != nil {
		t.Fatalf("expected unmount to succeed, got: %v", err)
	}
	if !strings.Contains(buf.String(), "successfully unmounted") {
		t.Errorf("unexpected output: %s", buf.String())
	}

	// 2. Test mount with NoFuse workspace (should return error)
	wsNoFuse := &config.WorkspaceConfig{
		WorkspaceID:  "ws-test-nofuse",
		Name:         "nofuse-ws",
		BaseCL:       1,
		RootTreeHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		MountDir:     filepath.Join(tempDir, "nofuse-mount"),
		ScratchDir:   filepath.Join(tempDir, "nofuse-scratch"),
		NoFuse:       true,
	}
	if err := mgr.Save(wsNoFuse); err != nil {
		t.Fatalf("failed to save nofuse ws: %v", err)
	}

	mountCmd := newWorkspaceMountCmd(rootFlags)
	mountCmd.SetArgs([]string{"nofuse-ws"})
	if err := mountCmd.Execute(); err == nil {
		t.Fatalf("expected error mounting nofuse workspace, got nil")
	}

	// 3. Test delete alias (destroy)
	deleteCmd := newWorkspaceDeleteCmd(rootFlags)
	delBuf := new(bytes.Buffer)
	deleteCmd.SetOut(delBuf)
	deleteCmd.SetErr(delBuf)
	deleteCmd.SetArgs([]string{"nofuse-ws"})
	if err := deleteCmd.Execute(); err != nil {
		t.Fatalf("failed to delete workspace: %v", err)
	}
	if !strings.Contains(delBuf.String(), "deleted successfully") {
		t.Errorf("unexpected output: %s", delBuf.String())
	}
}
