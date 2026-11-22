package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"bazil.org/fuse"
	fusefs "bazil.org/fuse/fs"
	"bazil.org/fuse/fs/fstestutil"
	"niebla/internal/cli/config"
)

func TestWorkspaceMount_PreservesScratchDirectory(t *testing.T) {
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
	defer func() { _ = unmountWorkspace(mountDir, true) }()

	testScratchFile := filepath.Join(scratchDir, "unshelved_work.txt")
	if err := os.WriteFile(testScratchFile, []byte("important user work"), 0644); err != nil {
		t.Fatalf("failed to write test scratch file: %v", err)
	}

	wsConfig := &config.WorkspaceConfig{
		WorkspaceID:  "ws-preserve-scratch",
		Name:         "preserve-ws",
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

	mountCmd := newWorkspaceMountCmd(rootFlags)
	mountCmd.SetArgs([]string{"preserve-ws"})
	_ = mountCmd.Execute()

	data, err := os.ReadFile(testScratchFile)
	if err != nil {
		t.Fatalf("expected scratch file %s to be preserved across mount, but it was deleted: %v", testScratchFile, err)
	}
	if string(data) != "important user work" {
		t.Errorf("scratch file content was corrupted, got: %s", string(data))
	}
}

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

func TestWorkspaceDelete_Force_MissingMetadata(t *testing.T) {
	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "workspaces")
	_ = os.MkdirAll(configDir, 0755)

	rootFlags := &RootFlags{
		ConfigDir: configDir,
	}

	// 1. Without --force, deleting missing workspace returns error
	delCmd := newWorkspaceDeleteCmd(rootFlags)
	delCmd.SetArgs([]string{"missing-ws"})
	if err := delCmd.Execute(); err == nil {
		t.Fatalf("expected error deleting non-existent workspace without --force, got nil")
	}

	// 2. With --force, deleting missing workspace succeeds idempotently
	delForceCmd := newWorkspaceDeleteCmd(rootFlags)
	buf := new(bytes.Buffer)
	delForceCmd.SetOut(buf)
	delForceCmd.SetErr(buf)
	delForceCmd.SetArgs([]string{"missing-ws", "--force"})
	if err := delForceCmd.Execute(); err != nil {
		t.Fatalf("expected delete --force to succeed on missing workspace, got: %v", err)
	}
	if !strings.Contains(buf.String(), "deleted successfully") {
		t.Errorf("unexpected output: %s", buf.String())
	}
}

func TestWorkspaceDelete_CleansMountDir(t *testing.T) {
	tempDir := t.TempDir()
	configDir := filepath.Join(tempDir, "workspaces")
	_ = os.MkdirAll(configDir, 0755)

	mgr, err := config.NewManager(configDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	mountDir := filepath.Join(tempDir, "test-mount")
	scratchDir := filepath.Join(tempDir, "test-scratch")
	_ = os.MkdirAll(mountDir, 0755)
	_ = os.MkdirAll(scratchDir, 0755)

	wsConfig := &config.WorkspaceConfig{
		WorkspaceID: "ws-clean-mount",
		Name:        "clean-mount-ws",
		BaseCL:      1,
		MountDir:    mountDir,
		ScratchDir:  scratchDir,
	}
	if err := mgr.Save(wsConfig); err != nil {
		t.Fatalf("failed to save ws config: %v", err)
	}

	rootFlags := &RootFlags{
		ConfigDir: configDir,
	}

	delCmd := newWorkspaceDeleteCmd(rootFlags)
	buf := new(bytes.Buffer)
	delCmd.SetOut(buf)
	delCmd.SetArgs([]string{"clean-mount-ws"})

	if err := delCmd.Execute(); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Verify MountDir is completely removed from disk
	if _, err := os.Stat(mountDir); !os.IsNotExist(err) {
		t.Errorf("expected mountDir %s to be removed, but it still exists", mountDir)
	}
}

func TestFuseMountHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_FUSE_HELPER") != "1" {
		return
	}
	mountDir := os.Getenv("FUSE_HELPER_MOUNT_DIR")
	if mountDir == "" {
		os.Exit(1)
	}
	c, err := fuse.Mount(mountDir)
	if err != nil {
		os.Exit(2)
	}
	server := fusefs.New(c, nil)
	_ = server.Serve(dummyFS{})
	os.Exit(0)
}

func TestWorkspaceDelete_Force_DanglingFUSEMountRecovery(t *testing.T) {
	if _, err := os.Stat("/dev/fuse"); os.IsNotExist(err) {
		t.Skip("skipping test: /dev/fuse not found")
	}

	tempDir := t.TempDir()
	mountDir := filepath.Join(tempDir, "dangling-mount")
	if err := os.MkdirAll(mountDir, 0755); err != nil {
		t.Fatalf("failed to create mount dir: %v", err)
	}

	// 1. Spawn separate helper process to hold FUSE mount
	cmd := exec.Command(os.Args[0], "-test.run=TestFuseMountHelperProcess")
	cmd.Env = append(os.Environ(), "GO_WANT_FUSE_HELPER=1", "FUSE_HELPER_MOUNT_DIR="+mountDir)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start FUSE helper process: %v", err)
	}

	// Wait for mount to become active in /proc/mounts
	deadline := time.Now().Add(3 * time.Second)
	mounted := false
	for time.Now().Before(deadline) {
		if isMountPoint(mountDir) {
			mounted = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !mounted {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Skip("FUSE mount helper did not mount in time (unprivileged or restricted environment)")
	}

	// 2. Abruptly kill the FUSE daemon process with SIGKILL to induce dangling mount state
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill FUSE helper: %v", err)
	}
	_ = cmd.Wait()

	// Verify that the mount point is dangling in /proc/mounts
	if !isMountPoint(mountDir) {
		t.Fatalf("expected %s to be listed in /proc/mounts as dangling mount", mountDir)
	}

	// 3. Register workspace config pointing to dangling mount
	configDir := filepath.Join(tempDir, "workspaces")
	_ = os.MkdirAll(configDir, 0755)
	mgr, err := config.NewManager(configDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	wsName := "dangling-ws"
	wsConfig := &config.WorkspaceConfig{
		WorkspaceID: "ws-dangling",
		Name:        wsName,
		BaseCL:      1,
		MountDir:    mountDir,
		ScratchDir:  filepath.Join(tempDir, "scratch"),
	}
	if err := mgr.Save(wsConfig); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	// 4. Run workspace delete --force
	rootFlags := &RootFlags{
		ConfigDir: configDir,
	}
	delCmd := newWorkspaceDeleteCmd(rootFlags)
	buf := new(bytes.Buffer)
	delCmd.SetOut(buf)
	delCmd.SetArgs([]string{wsName, "--force"})

	if err := delCmd.Execute(); err != nil {
		t.Fatalf("expected force delete to succeed on dangling mount, got: %v", err)
	}

	// 5. Verify cleanly detached and removed from disk
	if isMountPoint(mountDir) {
		t.Errorf("expected %s to be detached from /proc/mounts", mountDir)
	}
	if _, err := os.Stat(mountDir); !os.IsNotExist(err) {
		t.Errorf("expected directory %s to be removed from disk, got err: %v", mountDir, err)
	}
}

func TestWorkspaceDelete_Force_DanglingMount_And_MissingMetadata(t *testing.T) {
	if _, err := os.Stat("/dev/fuse"); os.IsNotExist(err) {
		t.Skip("skipping test: /dev/fuse not found")
	}

	tempDir := t.TempDir()
	tempHome := filepath.Join(tempDir, "home")
	_ = os.MkdirAll(tempHome, 0755)
	t.Setenv("HOME", tempHome)

	wsName := "orphan-dangling-ws"
	targetMountDir := filepath.Join(tempHome, "niebla", "workspaces", wsName)
	if err := os.MkdirAll(targetMountDir, 0755); err != nil {
		t.Fatalf("failed to create target mount dir: %v", err)
	}

	// 1. Spawn separate helper process to hold FUSE mount at default inferred path
	cmd := exec.Command(os.Args[0], "-test.run=TestFuseMountHelperProcess")
	cmd.Env = append(os.Environ(), "GO_WANT_FUSE_HELPER=1", "FUSE_HELPER_MOUNT_DIR="+targetMountDir)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start FUSE helper process: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	mounted := false
	for time.Now().Before(deadline) {
		if isMountPoint(targetMountDir) {
			mounted = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !mounted {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Skip("FUSE mount helper did not mount in time (unprivileged or restricted environment)")
	}

	// 2. Abruptly kill the FUSE daemon process with SIGKILL
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill helper: %v", err)
	}
	_ = cmd.Wait()

	if !isMountPoint(targetMountDir) {
		t.Fatalf("expected %s to be listed in /proc/mounts as dangling mount", targetMountDir)
	}

	// 3. Execute force delete with NO metadata JSON existing
	configDir := filepath.Join(tempDir, "empty-configs")
	_ = os.MkdirAll(configDir, 0755)
	rootFlags := &RootFlags{
		ConfigDir: configDir,
	}

	delCmd := newWorkspaceDeleteCmd(rootFlags)
	buf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	delCmd.SetOut(buf)
	delCmd.SetErr(errBuf)
	delCmd.SetArgs([]string{wsName, "--force"})

	if err := delCmd.Execute(); err != nil {
		t.Fatalf("expected force delete to succeed on orphaned dangling mount with missing metadata, got: %v", err)
	}
	t.Logf("stdout: %s", buf.String())
	t.Logf("stderr: %s", errBuf.String())

	// 4. Verify cleanly detached and removed from disk
	if isMountPoint(targetMountDir) {
		t.Errorf("expected %s to be detached from /proc/mounts", targetMountDir)
	}
	if _, err := os.Stat(targetMountDir); !os.IsNotExist(err) {
		t.Errorf("expected directory %s to be removed from disk, but it still exists", targetMountDir)
	}
}
