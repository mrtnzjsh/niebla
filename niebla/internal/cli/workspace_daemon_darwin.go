//go:build darwin

package cli

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Fallback to signal 0
	return syscall.Kill(pid, 0) == nil
}

func terminateProcess(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func isMountPoint(dir string) bool {
	if dir == "" {
		return false
	}
	cleanDir := filepath.Clean(dir)
	if abs, err := filepath.Abs(cleanDir); err == nil {
		cleanDir = abs
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(cleanDir, &stat); err == nil {
		mntonname := unix.ByteSliceToString(stat.Mntonname[:])
		return mntonname == cleanDir
	}
	return false
}

func unmountWorkspace(mountDir string, force ...bool) error {
	isForce := len(force) > 0 && force[0]
	var lastErr string

	// Try diskutil unmount
	if diskutilPath, err := exec.LookPath("diskutil"); err == nil {
		args := []string{"unmount"}
		if isForce {
			args = append(args, "force")
		}
		args = append(args, mountDir)
		cmd := exec.Command(diskutilPath, args...)
		if out, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else {
			lastErr = strings.TrimSpace(string(out))
		}
	}

	// Try umount
	if umountPath, err := exec.LookPath("umount"); err == nil {
		var args []string
		if isForce {
			args = append(args, "-f")
		}
		args = append(args, mountDir)
		cmd := exec.Command(umountPath, args...)
		if out, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else if lastErr == "" {
			lastErr = strings.TrimSpace(string(out))
		}
	}

	// Direct syscall unmount
	flags := 0
	if isForce {
		flags = unix.MNT_FORCE
	}
	if err := unix.Unmount(mountDir, flags); err == nil {
		return nil
	}

	if lastErr != "" {
		return fmt.Errorf("%s", lastErr)
	}
	return fmt.Errorf("operation not permitted")
}
