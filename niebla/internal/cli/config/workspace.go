package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var (
	// ErrWorkspaceNotFound is returned when a workspace config file does not exist.
	ErrWorkspaceNotFound = errors.New("workspace metadata not found")

	// ErrNotInWorkspace is returned when cwd is not inside any workspace mount.
	ErrNotInWorkspace = errors.New("not inside a Niebla workspace")
)

// WorkspaceConfig represents the metadata stored in ~/.niebla/workspaces/<name>.json
type WorkspaceConfig struct {
	WorkspaceID  string `json:"workspace_id"`
	Name         string `json:"name"`
	BaseCL       int64  `json:"base_cl"`
	RootTreeHash string `json:"root_tree_hash"`
	MountDir     string `json:"mount_dir"`
	ScratchDir   string `json:"scratch_dir"`
	PID          int    `json:"pid"`
	ActiveCL     int64  `json:"active_cl,omitempty"`
	NoFuse       bool   `json:"no_fuse,omitempty"`
	SocketPath   string `json:"socket_path,omitempty"`
}

// Manager handles reading, writing, and querying workspace configs.
type Manager struct {
	configDir string
}

// DefaultConfigDir returns the default ~/.niebla/workspaces directory.
func DefaultConfigDir() (string, error) {
	if envDir := os.Getenv("NIEBLA_CONFIG_DIR"); envDir != "" {
		return envDir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("unable to determine home directory: %w", err)
	}
	nieblaDir := filepath.Join(home, ".niebla", "workspaces")
	return nieblaDir, nil
}

// NewManager creates a manager for workspace configurations.
func NewManager(customDir string) (*Manager, error) {
	if customDir != "" {
		return &Manager{configDir: customDir}, nil
	}
	defaultDir, err := DefaultConfigDir()
	if err != nil {
		return nil, err
	}
	return &Manager{configDir: defaultDir}, nil
}

// ConfigDir returns the path to the workspace configuration directory.
func (m *Manager) ConfigDir() string {
	return m.configDir
}

// ConfigFilePath returns the absolute path to the configuration file for a workspace.
func (m *Manager) ConfigFilePath(name string) string {
	return filepath.Join(m.configDir, name+".json")
}

// Save writes the workspace configuration to disk.
func (m *Manager) Save(cfg *WorkspaceConfig) error {
	if cfg == nil || cfg.Name == "" {
		return errors.New("workspace config and name cannot be empty")
	}

	if err := os.MkdirAll(m.configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory %s: %w", m.configDir, err)
	}

	filePath := m.ConfigFilePath(cfg.Name)
	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return fmt.Errorf("failed to encode workspace json: %w", err)
	}

	// Write atomically via temporary file
	tmpPath := fmt.Sprintf("%s.tmp.%d", filePath, os.Getpid())
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write temporary workspace config: %w", err)
	}

	if err := os.Rename(tmpPath, filePath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to persist workspace config: %w", err)
	}

	return nil
}

// Load reads a workspace configuration by name.
func (m *Manager) Load(name string) (*WorkspaceConfig, error) {
	filePath := m.ConfigFilePath(name)
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %q", ErrWorkspaceNotFound, name)
		}
		return nil, fmt.Errorf("failed to read workspace config %s: %w", filePath, err)
	}

	var cfg WorkspaceConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse workspace config %s: %w", filePath, err)
	}

	return &cfg, nil
}

// Delete removes the workspace configuration file.
func (m *Manager) Delete(name string) error {
	filePath := m.ConfigFilePath(name)
	if err := os.Remove(filePath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to delete workspace config %s: %w", filePath, err)
	}
	return nil
}

// List returns all registered workspace configurations.
func (m *Manager) List() ([]*WorkspaceConfig, error) {
	entries, err := os.ReadDir(m.configDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []*WorkspaceConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read config directory %s: %w", m.configDir, err)
	}

	var configs []*WorkspaceConfig
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		cfg, err := m.Load(name)
		if err != nil {
			// Skip corrupt or unreadable files but proceed with the rest
			continue
		}
		configs = append(configs, cfg)
	}

	sort.Slice(configs, func(i, j int) bool {
		return configs[i].Name < configs[j].Name
	})

	return configs, nil
}

// ResolveWorkspace determines the active workspace either from an explicit name or from the current working directory.
func (m *Manager) ResolveWorkspace(explicitName, cwd string) (*WorkspaceConfig, error) {
	if explicitName != "" {
		return m.Load(explicitName)
	}

	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to determine current directory: %w", err)
		}
	}

	cleanCwd := filepath.Clean(cwd)
	evalCwd, err := filepath.EvalSymlinks(cleanCwd)
	if err != nil {
		evalCwd = cleanCwd
	}

	workspaces, err := m.List()
	if err != nil {
		return nil, err
	}

	// Look for a workspace whose mount directory matches or contains cwd
	var bestMatch *WorkspaceConfig
	bestMatchLen := -1

	for _, ws := range workspaces {
		cleanMount := filepath.Clean(ws.MountDir)
		evalMount, err := filepath.EvalSymlinks(cleanMount)
		if err != nil {
			evalMount = cleanMount
		}

		// Check both cleanMount and evalMount
		isMatch := isSubdirectoryOrEqual(cleanMount, cleanCwd) || isSubdirectoryOrEqual(evalMount, evalCwd)
		if isMatch {
			if len(cleanMount) > bestMatchLen {
				bestMatch = ws
				bestMatchLen = len(cleanMount)
			}
		}
	}

	if bestMatch != nil {
		return bestMatch, nil
	}

	return nil, fmt.Errorf("%w: cwd %q is not inside any registered Niebla workspace mount", ErrNotInWorkspace, cwd)
}

func isSubdirectoryOrEqual(parent, child string) bool {
	if parent == child {
		return true
	}
	prefix := parent + string(filepath.Separator)
	return strings.HasPrefix(child, prefix)
}
