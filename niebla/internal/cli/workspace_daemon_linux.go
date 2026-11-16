//go:build linux

package cli

import (
	"bufio"
	"fmt"
	"os"
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
	// Check /proc/<pid> on Linux
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		return true
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

	f, err := os.Open("/proc/mounts")
	if err != nil {
		// Fallback to checking via unix.Access
		return unix.Access(cleanDir, unix.F_OK) == nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 {
			mountTarget := filepath.Clean(fields[1])
			if absTarget, err := filepath.Abs(mountTarget); err == nil {
				mountTarget = absTarget
			}
			if mountTarget == cleanDir {
				return true
			}
		}
	}
	return false
}

func unmountWorkspace(mountDir string, force ...bool) error {
	isForce := len(force) > 0 && force[0]
	var lastErr string

	// Try fusermount3 first
	if fuserPath, err := exec.LookPath("fusermount3"); err == nil {
		if !isForce {
			cmd := exec.Command(fuserPath, "-u", mountDir)
			if out, err := cmd.CombinedOutput(); err == nil {
				return nil
			} else {
				lastErr = strings.TrimSpace(string(out))
			}
		}
		// Fallback to lazy unmount if busy, dangling, or force is requested
		lazyCmd := exec.Command(fuserPath, "-u", "-z", mountDir)
		if out, err := lazyCmd.CombinedOutput(); err == nil {
			return nil
		} else if lastErr == "" {
			lastErr = strings.TrimSpace(string(out))
		}
	}

	// Try fusermount
	if fuserPath, err := exec.LookPath("fusermount"); err == nil {
		if !isForce {
			cmd := exec.Command(fuserPath, "-u", mountDir)
			if out, err := cmd.CombinedOutput(); err == nil {
				return nil
			} else if lastErr == "" {
				lastErr = strings.TrimSpace(string(out))
			}
		}
		lazyCmd := exec.Command(fuserPath, "-u", "-z", mountDir)
		if out, err := lazyCmd.CombinedOutput(); err == nil {
			return nil
		} else if lastErr == "" {
			lastErr = strings.TrimSpace(string(out))
		}
	}

	// Direct syscall unmount
	if err := unix.Unmount(mountDir, 0); err == nil {
		return nil
	}

	if err := unix.Unmount(mountDir, unix.MNT_DETACH); err == nil {
		return nil
	}

	if err := unix.Unmount(mountDir, unix.MNT_FORCE|unix.MNT_DETACH); err == nil {
		return nil
	}

	if err := unix.Unmount(mountDir, unix.MNT_FORCE); err == nil {
		return nil
	}

	if lastErr != "" {
		return fmt.Errorf("%s", lastErr)
	}
	return fmt.Errorf("operation not permitted")
}
