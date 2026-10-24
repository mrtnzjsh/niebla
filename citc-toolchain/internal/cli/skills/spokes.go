package skills

import (
	"fmt"
	"os"
	"path/filepath"
)

// HarnessStatus captures the state of an agent harness spoke.
type HarnessStatus struct {
	Name        string
	SpokePath   string
	TargetPath  string
	HarnessRoot string
	Status      string
	Message     string
}

type harnessDef struct {
	name        string
	harnessRoot func(home string) string
	parentDir   func(home string) string
	spokePath   func(home string) string
	targetPath  func(hubDir string) string
	isFile      bool
}

func getHarnessDefs() []harnessDef {
	return []harnessDef{
		{
			name: "Antigravity / Gemini",
			harnessRoot: func(home string) string {
				return filepath.Join(home, ".gemini")
			},
			parentDir: func(home string) string {
				return filepath.Join(home, ".gemini", "config", "skills")
			},
			spokePath: func(home string) string {
				return filepath.Join(home, ".gemini", "config", "skills", "citc")
			},
			targetPath: func(hubDir string) string {
				return hubDir
			},
			isFile: false,
		},
		{
			name: "Claude Code",
			harnessRoot: func(home string) string {
				return filepath.Join(home, ".claude")
			},
			parentDir: func(home string) string {
				return filepath.Join(home, ".claude", "skills")
			},
			spokePath: func(home string) string {
				return filepath.Join(home, ".claude", "skills", "citc")
			},
			targetPath: func(hubDir string) string {
				return hubDir
			},
			isFile: false,
		},
		{
			name: "Universal .agents",
			harnessRoot: func(home string) string {
				return filepath.Join(home, ".config", "agents")
			},
			parentDir: func(home string) string {
				return filepath.Join(home, ".config", "agents", "skills")
			},
			spokePath: func(home string) string {
				return filepath.Join(home, ".config", "agents", "skills", "citc")
			},
			targetPath: func(hubDir string) string {
				return hubDir
			},
			isFile: false,
		},
		{
			name: "Cursor Rules",
			harnessRoot: func(home string) string {
				return filepath.Join(home, ".cursor")
			},
			parentDir: func(home string) string {
				return filepath.Join(home, ".cursor", "rules")
			},
			spokePath: func(home string) string {
				return filepath.Join(home, ".cursor", "rules", "citc.mdc")
			},
			targetPath: func(hubDir string) string {
				return filepath.Join(hubDir, "SKILL.md")
			},
			isFile: true,
		},
	}
}

// CheckSpokes inspects all harness spoke symlinks and returns their current status.
func CheckSpokes(hubDir string, customHome string) ([]HarnessStatus, error) {
	home := customHome
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("unable to determine home directory: %w", err)
		}
	}

	defs := getHarnessDefs()
	statuses := make([]HarnessStatus, 0, len(defs))

	for _, def := range defs {
		spokePath := def.spokePath(home)
		targetPath := def.targetPath(hubDir)
		harnessRoot := def.harnessRoot(home)

		st := HarnessStatus{
			Name:        def.name,
			SpokePath:   spokePath,
			TargetPath:  targetPath,
			HarnessRoot: harnessRoot,
		}

		info, err := os.Lstat(spokePath)
		if err != nil {
			if os.IsNotExist(err) {
				if _, rErr := os.Stat(harnessRoot); rErr == nil {
					st.Status = "UNLINKED"
					st.Message = "Harness installed but spoke symlink is missing"
				} else {
					st.Status = "NOT_INSTALLED"
					st.Message = "Harness directory not found on system"
				}
			} else {
				st.Status = "ERROR"
				st.Message = err.Error()
			}
			statuses = append(statuses, st)
			continue
		}

		if info.Mode()&os.ModeSymlink == 0 {
			st.Status = "NOT_A_SYMLINK"
			st.Message = "Path exists but is a regular file or directory instead of symlink"
			statuses = append(statuses, st)
			continue
		}

		linkTarget, err := os.Readlink(spokePath)
		if err != nil {
			st.Status = "BROKEN_LINK"
			st.Message = fmt.Sprintf("Failed to read symlink: %v", err)
			statuses = append(statuses, st)
			continue
		}

		cleanLink := filepath.Clean(linkTarget)
		cleanTarget := filepath.Clean(targetPath)
		if cleanLink == cleanTarget {
			st.Status = "LINKED"
			st.Message = "Active and pointing to Hub"
		} else {
			st.Status = "STALE"
			st.Message = fmt.Sprintf("Points to %s instead of %s", cleanLink, cleanTarget)
		}

		statuses = append(statuses, st)
	}

	return statuses, nil
}

// SyncSpokes links the Hub into harness spokes.
// If proactive is true, it creates missing harness parent directories.
// If proactive is false (opportunistic), it only links harnesses whose root directory already exists.
func SyncSpokes(hubDir string, proactive bool, customHome string) ([]HarnessStatus, error) {
	home := customHome
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("unable to determine home directory: %w", err)
		}
	}

	defs := getHarnessDefs()
	statuses := make([]HarnessStatus, 0, len(defs))

	for _, def := range defs {
		spokePath := def.spokePath(home)
		targetPath := def.targetPath(hubDir)
		parentDir := def.parentDir(home)
		harnessRoot := def.harnessRoot(home)

		st := HarnessStatus{
			Name:        def.name,
			SpokePath:   spokePath,
			TargetPath:  targetPath,
			HarnessRoot: harnessRoot,
		}

		harnessExists := false
		if _, err := os.Stat(harnessRoot); err == nil {
			harnessExists = true
		}

		// In non-proactive mode, skip harnesses that aren't installed yet
		if !proactive && !harnessExists {
			st.Status = "SKIPPED"
			st.Message = "Harness not installed"
			statuses = append(statuses, st)
			continue
		}

		if err := os.MkdirAll(parentDir, 0755); err != nil {
			st.Status = "ERROR"
			st.Message = fmt.Sprintf("Failed to create parent directory: %v", err)
			statuses = append(statuses, st)
			continue
		}

		// Check existing symlink
		if info, err := os.Lstat(spokePath); err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				existingTarget, err := os.Readlink(spokePath)
				if err == nil && filepath.Clean(existingTarget) == filepath.Clean(targetPath) {
					st.Status = "LINKED"
					st.Message = "Already up to date"
					statuses = append(statuses, st)
					continue
				}
			}
			// Remove stale or conflicting path
			_ = os.Remove(spokePath)
		}

		// Create atomic symlink
		tmpLink := fmt.Sprintf("%s.tmp.%d", spokePath, os.Getpid())
		_ = os.Remove(tmpLink)

		if err := os.Symlink(targetPath, tmpLink); err != nil {
			st.Status = "ERROR"
			st.Message = fmt.Sprintf("Failed to create symlink: %v", err)
			statuses = append(statuses, st)
			continue
		}

		if err := os.Rename(tmpLink, spokePath); err != nil {
			_ = os.Remove(tmpLink)
			st.Status = "ERROR"
			st.Message = fmt.Sprintf("Failed to atomically replace symlink: %v", err)
			statuses = append(statuses, st)
			continue
		}

		st.Status = "LINKED"
		st.Message = "Successfully linked to Hub"
		statuses = append(statuses, st)
	}

	return statuses, nil
}
