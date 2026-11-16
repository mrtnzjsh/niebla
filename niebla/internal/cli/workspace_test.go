package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bazil.org/fuse"
	fusefs "bazil.org/fuse/fs"
	"bazil.org/fuse/fs/fstestutil"
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

type dummyFS struct{}

func (dummyFS) Root() (fusefs.Node, error) {
	return dummyDir{}, nil
}

type dummyDir struct{}

func (dummyDir) Attr(ctx context.Context, a *fuse.Attr) error {
	a.Mode = os.ModeDir | 0755
	return nil
}

func TestUnmountWorkspace_FailureMode_PreservesError(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Non-existent path
	nonExistent := filepath.Join(tempDir, "does-not-exist")
	err := unmountWorkspace(nonExistent)
	if err == nil {
		t.Fatalf("expected error unmounting non-existent path, got nil")
	}
	if strings.Contains(strings.ToLower(err.Error()), "operation not permitted") {
		t.Errorf("error was masked as 'operation not permitted', expected fusermount error: %v", err)
	}

	// 2. Unmounted regular directory
	regularDir := filepath.Join(tempDir, "empty-dir")
	if err := os.Mkdir(regularDir, 0755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}
	err = unmountWorkspace(regularDir)
	if err == nil {
		t.Fatalf("expected error unmounting regular directory, got nil")
	}
	if strings.Contains(strings.ToLower(err.Error()), "operation not permitted") {
		t.Errorf("error was masked as 'operation not permitted', expected fusermount error: %v", err)
	}
}

func TestUnmountWorkspace_BusyMount_LazyFallback(t *testing.T) {
	if _, err := os.Stat("/dev/fuse"); os.IsNotExist(err) {
		t.Skip("skipping test: /dev/fuse not found")
	}

	mnt, err := fstestutil.MountedT(t, dummyFS{}, nil)
	if err != nil {
		t.Skipf("skipping live FUSE mount test: %v", err)
	}

	// Open directory handle inside mount point to simulate a process (e.g. IDE or bash) holding cwd
	f, err := os.Open(mnt.Dir)
	if err != nil {
		_ = mnt.Conn.Close()
		t.Fatalf("failed to open mounted dir: %v", err)
	}

	// unmountWorkspace should detect busy on standard -u and succeed via lazy fallback (-u -z)
	if err := unmountWorkspace(mnt.Dir); err != nil {
		_ = f.Close()
		_ = mnt.Conn.Close()
		t.Fatalf("expected unmountWorkspace to succeed via lazy fallback, got: %v", err)
	}

	// Close open file handle
	_ = f.Close()
	// Close connection and clean up mount dir (without calling mnt.Close() which retries standard unmount)
	_ = mnt.Conn.Close()
	<-mnt.Error
	_ = os.Remove(mnt.Dir)
}

