package preflight

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// CheckStatus represents the outcome of an individual pre-flight verification check.
type CheckStatus string

const (
	StatusPass CheckStatus = "PASS"
	StatusWarn CheckStatus = "WARN"
	StatusFail CheckStatus = "FAIL"
)

// CheckItem represents the result of a single system component inspection.
type CheckItem struct {
	Name    string      `json:"name"`
	Status  CheckStatus `json:"status"`
	Message string      `json:"message"`
	Detail  string      `json:"detail,omitempty"`
}

// Report aggregates all FUSE environment inspections.
type Report struct {
	Ready    bool        `json:"ready"`
	Checks   []CheckItem `json:"checks"`
	Warnings []string    `json:"warnings,omitempty"`
	Errors   []string    `json:"errors,omitempty"`
}

// CheckDevFuse verifies read/write accessibility of the FUSE device node.
func CheckDevFuse(devPath string) CheckItem {
	if devPath == "" {
		devPath = "/dev/fuse"
	}

	info, err := os.Stat(devPath)
	if err != nil {
		return CheckItem{
			Name:    "FUSE Device Node (/dev/fuse)",
			Status:  StatusFail,
			Message: fmt.Sprintf("%s is missing or inaccessible: %v", devPath, err),
			Detail:  "Ensure the FUSE kernel module is loaded ('modprobe fuse') and the container or VM has access to the device node.",
		}
	}

	if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice == 0 {
		return CheckItem{
			Name:    "FUSE Device Node (/dev/fuse)",
			Status:  StatusWarn,
			Message: fmt.Sprintf("%s is not a character device", devPath),
			Detail:  "Unexpected device mode. FUSE operations may fail.",
		}
	}

	// Attempt to open read-write or read-only
	f, err := os.OpenFile(devPath, os.O_RDWR, 0)
	if err != nil {
		f2, err2 := os.Open(devPath)
		if err2 != nil {
			return CheckItem{
				Name:    "FUSE Device Node (/dev/fuse)",
				Status:  StatusFail,
				Message: fmt.Sprintf("Permission denied opening %s: %v", devPath, err),
				Detail:  "User does not have read/write permissions to /dev/fuse. Add user to 'fuse' group or verify udev rules.",
			}
		}
		_ = f2.Close()
		return CheckItem{
			Name:    "FUSE Device Node (/dev/fuse)",
			Status:  StatusWarn,
			Message: fmt.Sprintf("%s is only accessible read-only", devPath),
			Detail:  "FUSE mounting requires read/write access. Ensure user permissions allow writing to /dev/fuse.",
		}
	}
	_ = f.Close()

	return CheckItem{
		Name:    "FUSE Device Node (/dev/fuse)",
		Status:  StatusPass,
		Message: fmt.Sprintf("%s is accessible with read/write permissions", devPath),
	}
}

// CheckFuseConf parses /etc/fuse.conf to verify that user_allow_other is enabled.
func CheckFuseConf(confPath string) CheckItem {
	if confPath == "" {
		confPath = "/etc/fuse.conf"
	}

	f, err := os.Open(confPath)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckItem{
				Name:    "FUSE Configuration (/etc/fuse.conf)",
				Status:  StatusWarn,
				Message: fmt.Sprintf("%s does not exist", confPath),
				Detail:  "On NixOS, configure 'programs.fuse.enable = true;' and 'programs.fuse.userAllowOther = true;'. On other distributions, create /etc/fuse.conf with 'user_allow_other'.",
			}
		}
		return CheckItem{
			Name:    "FUSE Configuration (/etc/fuse.conf)",
			Status:  StatusWarn,
			Message: fmt.Sprintf("Failed to read %s: %v", confPath, err),
		}
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	hasUserAllowOther := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "user_allow_other" {
			hasUserAllowOther = true
			break
		}
	}

	if !hasUserAllowOther {
		return CheckItem{
			Name:    "FUSE Configuration (/etc/fuse.conf)",
			Status:  StatusWarn,
			Message: "'user_allow_other' is not enabled in /etc/fuse.conf",
			Detail:  "Non-root mounting with 'allow_other' may fail. In NixOS, set 'programs.fuse.userAllowOther = true;'.",
		}
	}

	return CheckItem{
		Name:    "FUSE Configuration (/etc/fuse.conf)",
		Status:  StatusPass,
		Message: "'user_allow_other' is enabled in /etc/fuse.conf",
	}
}

// CheckFusermount locates fusermount/fusermount3 and verifies its setuid root permissions.
func CheckFusermount(explicitPath string) CheckItem {
	candidates := []string{}
	if explicitPath != "" {
		candidates = append(candidates, explicitPath)
	}

	// Priority locations (NixOS wrappers take precedence over unprivileged store binaries)
	candidates = append(candidates,
		"/run/wrappers/bin/fusermount3",
		"/run/wrappers/bin/fusermount",
	)

	if p, err := exec.LookPath("fusermount3"); err == nil {
		candidates = append(candidates, p)
	}
	if p, err := exec.LookPath("fusermount"); err == nil {
		candidates = append(candidates, p)
	}

	var foundPath string
	var foundInfo os.FileInfo

	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		if info, err := os.Stat(cand); err == nil {
			foundPath = cand
			foundInfo = info
			break
		}
	}

	if foundPath == "" {
		return CheckItem{
			Name:    "FUSE Mount Helper (fusermount3)",
			Status:  StatusFail,
			Message: "fusermount3 binary not found in PATH or /run/wrappers/bin",
			Detail:  "Install fuse3 package or on NixOS enable 'programs.fuse.enable = true;'.",
		}
	}

	isSetuid := (foundInfo.Mode() & os.ModeSetuid) != 0
	isRootOwned := isFileRootOwned(foundInfo)

	if !isSetuid || !isRootOwned {
		return CheckItem{
			Name:    "FUSE Mount Helper (fusermount3)",
			Status:  StatusWarn,
			Message: fmt.Sprintf("%s is missing setuid root permissions (mode: %v, root-owned: %v)", foundPath, foundInfo.Mode().Perm(), isRootOwned),
			Detail:  "Unprivileged users will receive 'mount failed: Operation not permitted'. On NixOS, ensure 'programs.fuse.enable = true;' is configured.",
		}
	}

	return CheckItem{
		Name:    "FUSE Mount Helper (fusermount3)",
		Status:  StatusPass,
		Message: fmt.Sprintf("%s is setuid root (%v)", foundPath, foundInfo.Mode().Perm()),
	}
}

// VerifyFUSEReadiness performs the full suite of FUSE environment checks using system defaults.
func VerifyFUSEReadiness() Report {
	devCheck := CheckDevFuse("/dev/fuse")
	confCheck := CheckFuseConf("/etc/fuse.conf")
	mountCheck := CheckFusermount("")

	checks := []CheckItem{devCheck, confCheck, mountCheck}
	var warnings []string
	var errors []string
	isReady := true

	for _, c := range checks {
		switch c.Status {
		case StatusFail:
			isReady = false
			errors = append(errors, fmt.Sprintf("%s: %s", c.Name, c.Message))
		case StatusWarn:
			warnings = append(warnings, fmt.Sprintf("%s: %s", c.Name, c.Message))
		}
	}

	return Report{
		Ready:    isReady,
		Checks:   checks,
		Warnings: warnings,
		Errors:   errors,
	}
}

// FormatDiagnostics renders a human-readable diagnostic summary suitable for CLI warnings.
func FormatDiagnostics(r Report) string {
	var sb strings.Builder
	for _, c := range r.Checks {
		icon := "✓"
		if c.Status == StatusWarn {
			icon = "⚠"
		} else if c.Status == StatusFail {
			icon = "✗"
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s: %s\n", icon, c.Name, c.Message))
		if c.Detail != "" && c.Status != StatusPass {
			sb.WriteString(fmt.Sprintf("      Hint: %s\n", c.Detail))
		}
	}
	return sb.String()
}
