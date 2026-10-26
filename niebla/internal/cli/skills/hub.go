package skills

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultHubDir returns the canonical XDG data directory for niebla skills:
// $XDG_DATA_HOME/niebla/skills/niebla (defaults to ~/.local/share/niebla/skills/niebla)
func DefaultHubDir() (string, error) {
	if env := os.Getenv("NIEBLA_SKILLS_HUB"); env != "" {
		return filepath.Abs(env)
	}

	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("unable to determine user home directory: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}

	return filepath.Join(dataHome, "niebla", "skills", "niebla"), nil
}

// EnsureHub verifies or creates the canonical Hub skill directory with latest assets.
func EnsureHub(customHubDir string) (string, error) {
	hubDir := customHubDir
	if hubDir == "" {
		var err error
		hubDir, err = DefaultHubDir()
		if err != nil {
			return "", err
		}
	}

	hubDir, err := filepath.Abs(hubDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute hub directory: %w", err)
	}

	if err := ExtractEmbeddedSkills(hubDir); err != nil {
		return "", fmt.Errorf("failed to populate hub skill directory %s: %w", hubDir, err)
	}

	return hubDir, nil
}
