//go:build windows

package cli

import (
	"os"
	"os/exec"
)

func detachProcess(cmd *exec.Cmd) {
	// Windows process detached flags or default
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	return err == nil && p != nil
}

func terminateProcess(pid int) {
	if pid <= 0 {
		return
	}
	p, err := os.FindProcess(pid)
	if err == nil && p != nil {
		_ = p.Kill()
	}
}

func isMountPoint(dir string) bool {
	return false
}

func unmountWorkspace(mountDir string, force ...bool) error {
	return nil
}
