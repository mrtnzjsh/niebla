package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"niebla/pkg/nieblafs/control"
)

func TestDaemonControlSocketReanchor(t *testing.T) {
	if os.Getenv("CI") != "" || testing.Short() {
		t.Skip("skipping daemon integration test in short mode")
	}

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "nieblafs.sock")
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	initialTree := "0123456789abcdef"

	daemonCmd := exec.Command("go", "run", "../main.go",
		"--scratch", scratchDir,
		"--cache", cacheDir,
		"--tree-hash", initialTree,
		"--socket", socketPath,
	)

	if err := daemonCmd.Start(); err != nil {
		t.Fatalf("failed to start nieblafs daemon: %v", err)
	}
	defer func() {
		if daemonCmd.Process != nil {
			_ = daemonCmd.Process.Signal(syscall.SIGTERM)
			_ = daemonCmd.Wait()
		}
	}()

	client := control.NewClient(socketPath)

	// Wait for control socket to become available
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		if err := client.Ping(ctx); err == nil {
			ready = true
			cancel()
			break
		}
		cancel()
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("timed out waiting for nieblafs control socket on %s", socketPath)
	}

	// Check status
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	statusResp, err := client.Status(ctx)
	if err != nil {
		t.Fatalf("failed to get daemon status: %v", err)
	}
	if statusResp.RootTreeHash != initialTree {
		t.Errorf("expected initial root tree %s, got %s", initialTree, statusResp.RootTreeHash)
	}

	// Trigger reanchor
	newTree := "fedcba9876543210"
	reanchorResp, err := client.Reanchor(ctx, newTree, 42)
	if err != nil {
		t.Fatalf("failed to reanchor: %v", err)
	}
	if reanchorResp.RootTreeHash != newTree {
		t.Errorf("expected reanchored tree %s, got %s", newTree, reanchorResp.RootTreeHash)
	}
	if reanchorResp.CLID != 42 {
		t.Errorf("expected CLID 42, got %d", reanchorResp.CLID)
	}

	// Verify updated status
	statusResp2, err := client.Status(ctx)
	if err != nil {
		t.Fatalf("failed to get daemon status after reanchor: %v", err)
	}
	if statusResp2.RootTreeHash != newTree {
		t.Errorf("expected status root tree %s, got %s", newTree, statusResp2.RootTreeHash)
	}
	if statusResp2.CLID != 42 {
		t.Errorf("expected status CLID 42, got %d", statusResp2.CLID)
	}

	// Signal graceful shutdown
	_ = daemonCmd.Process.Signal(syscall.SIGTERM)
	err = daemonCmd.Wait()
	if err != nil && daemonCmd.ProcessState != nil && daemonCmd.ProcessState.ExitCode() != 0 {
		t.Logf("daemon exited: %v (exit code %d)", err, daemonCmd.ProcessState.ExitCode())
	}
}
