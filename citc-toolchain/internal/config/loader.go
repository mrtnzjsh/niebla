package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

//go:embed assets/base_config.toml
var BaseConfigBytes []byte

// DefaultConfigPath determines the active configuration file path.
// Precedence:
// 1. CITC_CONFIG environment variable (if non-empty)
// 2. $XDG_CONFIG_HOME/citc-toolchain/config.toml
// 3. ~/.config/citc-toolchain/config.toml
func DefaultConfigPath() (string, error) {
	if custom := os.Getenv("CITC_CONFIG"); custom != "" {
		return custom, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "citc-toolchain", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine user home directory: %w", err)
	}
	return filepath.Join(home, ".config", "citc-toolchain", "config.toml"), nil
}

// Load loads the active configuration from the default path, initializing it from base
// defaults if missing, expanding environment variables, and validating the result.
// Fails fast with descriptive error messages if any configuration is invalid.
func Load() (*Config, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	return LoadFromPath(path)
}

// LoadFromPath loads configuration from a specific file path.
// If the file does not exist, it initializes it from the embedded base configuration.
// It parses the file, merges with baseline defaults, expands $USER / $HOME, and validates.
func LoadFromPath(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create config directory %s: %w", dir, err)
		}
		if err := os.WriteFile(path, BaseConfigBytes, 0644); err != nil {
			return nil, fmt.Errorf("failed to write default config to %s: %w", path, err)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	cfg, err := LoadBytes(data)
	if err != nil {
		var decodeErr *toml.DecodeError
		if errors.As(err, &decodeErr) {
			row, col := decodeErr.Position()
			return nil, fmt.Errorf("configuration syntax error in %s (line %d, column %d):\n  %s\nHint: Verify TOML syntax (check for unclosed quotes, invalid types, or missing '=').", path, row, col, decodeErr.Error())
		}
		return nil, fmt.Errorf("failed to load configuration from %s:\n%w", path, err)
	}

	return cfg, nil
}

// LoadBytes parses raw TOML configuration bytes, applies baseline defaults,
// expands environment variables, and executes fast-fail validation.
func LoadBytes(data []byte) (*Config, error) {
	// 1. Initialize with baseline defaults
	cfg := DefaultConfig()

	// 2. Unmarshal base config to ensure any structured defaults are primed
	if len(BaseConfigBytes) > 0 {
		if err := toml.Unmarshal(BaseConfigBytes, cfg); err != nil {
			return nil, fmt.Errorf("internal error decoding base configuration: %w", err)
		}
	}

	// 3. Unmarshal user TOML bytes (user values strictly take precedence)
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// 4. Expand environment variables ($USER, $HOME, etc.)
	expandConfigEnv(cfg)

	// 5. Fail fast on invalid configuration
	if err := Validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// expandConfigEnv resolves dynamic environment variables across string fields.
func expandConfigEnv(cfg *Config) {
	cfg.Core.ServerAddr = os.ExpandEnv(cfg.Core.ServerAddr)
	cfg.Server.Host = os.ExpandEnv(cfg.Server.Host)
	cfg.Server.DBURL = os.ExpandEnv(cfg.Server.DBURL)
	cfg.Server.BlobPath = os.ExpandEnv(cfg.Server.BlobPath)
	cfg.FS.MountBase = os.ExpandEnv(cfg.FS.MountBase)
	cfg.FS.CacheDir = os.ExpandEnv(cfg.FS.CacheDir)
	cfg.Critique.Host = os.ExpandEnv(cfg.Critique.Host)

	reviewer := strings.TrimSpace(os.ExpandEnv(cfg.Critique.DefaultReviewer))
	if reviewer == "" || reviewer == "$USER" {
		if u := os.Getenv("USER"); u != "" {
			reviewer = u
		} else if cur, err := user.Current(); err == nil && cur.Username != "" {
			reviewer = cur.Username
		} else {
			reviewer = "reviewer"
		}
	}
	cfg.Critique.DefaultReviewer = reviewer
}
