package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

//go:embed assets/base_config.toml
var BaseConfigBytes []byte

// NamedConfigBytes associates a source filename with its raw TOML content.
type NamedConfigBytes struct {
	Name string
	Data []byte
}

// SectionClaim records a TOML table header or subsection claimed by a file.
type SectionClaim struct {
	Section string
	File    string
	Line    int
}

// DefaultConfigPath determines the primary configuration file path.
// Precedence:
// 1. NIEBLA_CONFIG environment variable (if non-empty)
// 2. $XDG_CONFIG_HOME/niebla/config.toml
// 3. ~/.config/niebla/config.toml
func DefaultConfigPath() (string, error) {
	if custom := os.Getenv("NIEBLA_CONFIG"); custom != "" {
		info, err := os.Stat(custom)
		if err == nil && info.IsDir() {
			return filepath.Join(custom, "config.toml"), nil
		}
		return custom, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "niebla", "config.toml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine user home directory: %w", err)
	}
	return filepath.Join(home, ".config", "niebla", "config.toml"), nil
}

// DefaultConfigDir returns the active configuration directory (e.g. ~/.config/niebla).
func DefaultConfigDir() (string, error) {
	p, err := DefaultConfigPath()
	if err != nil {
		return "", err
	}
	info, err := os.Stat(p)
	if err == nil && info.IsDir() {
		return p, nil
	}
	return filepath.Dir(p), nil
}

// DiscoverConfigFiles returns all non-hidden *.toml files in dir, ordered with
// "config.toml" first (if present) followed by all other *.toml files sorted alphabetically.
func DiscoverConfigFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var primary string
	var others []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(name), ".toml") {
			continue
		}
		fullPath := filepath.Join(dir, name)
		if name == "config.toml" {
			primary = fullPath
		} else {
			others = append(others, fullPath)
		}
	}

	sort.Strings(others)
	var files []string
	if primary != "" {
		files = append(files, primary)
	}
	files = append(files, others...)
	return files, nil
}

// Load loads the active configuration from the default configuration directory,
// initializing config.toml from base defaults if no TOML files exist yet, combining
// all *.toml files in the directory, enforcing header/subsection uniqueness across files,
// expanding environment variables, and validating the result.
func Load() (*Config, error) {
	path, err := DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	return LoadFromPath(path)
}

// LoadFromPath loads configuration from a specific path.
// - If path is a directory, all *.toml files in that directory are combined.
// - If path is "config.toml" or resides in a directory alongside "config.toml" (or the active
//   DefaultConfigDir), all sibling *.toml files in that directory are discovered and combined.
// - If path is a standalone file in another directory without a sibling "config.toml"
//   (e.g. /tmp/candidate_config.toml), only that file is loaded and validated.
func LoadFromPath(path string) (*Config, error) {
	info, statErr := os.Stat(path)
	if statErr == nil && info.IsDir() {
		return LoadFromDir(path)
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)

	if os.IsNotExist(statErr) {
		if base == "config.toml" {
			existing, _ := DiscoverConfigFiles(dir)
			if len(existing) == 0 {
				if err := os.MkdirAll(dir, 0755); err != nil {
					return nil, fmt.Errorf("failed to create config directory %s: %w", dir, err)
				}
				if err := os.WriteFile(path, BaseConfigBytes, 0644); err != nil {
					return nil, fmt.Errorf("failed to write default config to %s: %w", path, err)
				}
			}
			return LoadFromDir(dir)
		}
		return nil, fmt.Errorf("failed to read config file %s: %w", path, statErr)
	}
	if statErr != nil {
		return nil, fmt.Errorf("failed to stat config file %s: %w", path, statErr)
	}

	// Determine whether this file should be combined with sibling *.toml files in dir.
	combineDir := false
	if base == "config.toml" {
		combineDir = true
	} else if defDir, err := DefaultConfigDir(); err == nil && filepath.Clean(dir) == filepath.Clean(defDir) {
		combineDir = true
	} else if _, err := os.Stat(filepath.Join(dir, "config.toml")); err == nil {
		combineDir = true
	}

	if combineDir {
		return LoadFromDir(dir)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	cfg, err := LoadMultiBytes([]NamedConfigBytes{{Name: path, Data: data}})
	if err != nil {
		return nil, formatFileLoadError(path, err)
	}
	return cfg, nil
}

// LoadFromDir discovers all *.toml files in dir, enforces header/subsection uniqueness
// across files, combines them into a single Config, expands environment variables, and validates.
func LoadFromDir(dir string) (*Config, error) {
	files, err := DiscoverConfigFiles(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to read config directory %s: %w", dir, err)
	}

	if len(files) == 0 {
		defaultFile := filepath.Join(dir, "config.toml")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create config directory %s: %w", dir, err)
		}
		if err := os.WriteFile(defaultFile, BaseConfigBytes, 0644); err != nil {
			return nil, fmt.Errorf("failed to write default config to %s: %w", defaultFile, err)
		}
		files = []string{defaultFile}
	}

	named := make([]NamedConfigBytes, 0, len(files))
	for _, fpath := range files {
		data, err := os.ReadFile(fpath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file %s: %w", fpath, err)
		}
		named = append(named, NamedConfigBytes{
			Name: filepath.Base(fpath),
			Data: data,
		})
	}

	cfg, err := LoadMultiBytes(named)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration from %s:\n%w", dir, err)
	}
	return cfg, nil
}

// LoadBytes parses raw TOML configuration bytes, applies baseline defaults,
// expands environment variables, and executes fast-fail validation.
func LoadBytes(data []byte) (*Config, error) {
	return LoadMultiBytes([]NamedConfigBytes{{Name: "", Data: data}})
}

// LoadMultiBytes combines multiple named TOML configuration payloads into a unified Config.
// It enforces that no header or subsection (e.g. [server], [critique.theme], [critique.ai])
// is defined in more than one file.
func LoadMultiBytes(files []NamedConfigBytes) (*Config, error) {
	// 1. Initialize with baseline defaults
	cfg := DefaultConfig()

	// 2. Unmarshal base config to ensure any structured defaults are primed
	if len(BaseConfigBytes) > 0 {
		if err := toml.Unmarshal(BaseConfigBytes, cfg); err != nil {
			return nil, fmt.Errorf("internal error decoding base configuration: %w", err)
		}
	}

	// 3. Verify TOML syntax of each file and enforce header/subsection uniqueness across files
	claimedBy := make(map[string]SectionClaim)
	var dupErrors ValidationErrors

	for _, f := range files {
		claims, err := ExtractTOMLSections(f.Name, f.Data)
		if err != nil {
			return nil, err
		}
		for _, claim := range claims {
			if prev, exists := claimedBy[claim.Section]; exists && prev.File != claim.File {
				dupErrors = append(dupErrors, ValidationError{
					Field:   claim.Section,
					Value:   fmt.Sprintf("[%s]", claim.Section),
					Message: fmt.Sprintf("duplicate configuration section [%s] defined in multiple files (%s:%d and %s:%d)", claim.Section, prev.File, prev.Line, claim.File, claim.Line),
					Hint:    fmt.Sprintf("Each header/subsection may only be defined in a single TOML file inside ~/.config/niebla/. Consolidate [%s] into either %s or %s.", claim.Section, prev.File, claim.File),
				})
			} else if !exists {
				claimedBy[claim.Section] = claim
			}
		}
	}

	if len(dupErrors) > 0 {
		return nil, dupErrors
	}

	// 4. Unmarshal each file in order into the unified Config struct
	var aiEndpointConfigured bool
	var aiExplicitEnabled *bool

	for _, f := range files {
		if err := toml.Unmarshal(f.Data, cfg); err != nil {
			var decodeErr *toml.DecodeError
			if errors.As(err, &decodeErr) && f.Name != "" {
				row, col := decodeErr.Position()
				return nil, fmt.Errorf("configuration syntax error in %s (line %d, column %d):\n  %s\nHint: Verify TOML syntax (check for unclosed quotes, invalid types, or missing '=').", f.Name, row, col, decodeErr.Error())
			}
			return nil, err
		}

		var rawProbe struct {
			Critique struct {
				AI struct {
					Enabled  *bool   `toml:"enabled"`
					Endpoint *string `toml:"endpoint"`
				} `toml:"ai"`
			} `toml:"critique"`
		}
		if err := toml.Unmarshal(f.Data, &rawProbe); err == nil {
			if rawProbe.Critique.AI.Endpoint != nil && strings.TrimSpace(*rawProbe.Critique.AI.Endpoint) != "" {
				aiEndpointConfigured = true
			}
			if rawProbe.Critique.AI.Enabled != nil {
				aiExplicitEnabled = rawProbe.Critique.AI.Enabled
			}
		}
	}

	// If the user configured an AI endpoint in any TOML file and did not explicitly set enabled = false,
	// enable the AI hint subsystem automatically.
	if aiEndpointConfigured && aiExplicitEnabled == nil {
		cfg.Critique.AI.Enabled = true
	}

	// 5. Expand environment variables ($USER, $HOME, etc.)
	expandConfigEnv(cfg)

	// 6. Fail fast on invalid configuration
	if err := Validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// ExtractTOMLSections parses raw TOML bytes and returns all table headers and subsections
// (e.g. "core", "critique.theme", "critique.ai") claimed by the file.
// It detects both explicit [header] / [[header]] declarations and inline/dotted sub-tables
// (e.g. `ai = { ... }` or `ai.endpoint = "..."` inside `[critique]`).
func ExtractTOMLSections(filename string, data []byte) ([]SectionClaim, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	var claims []SectionClaim
	seenInFile := make(map[string]bool)

	addClaim := func(sec string, lineNum int) {
		sec = normalizeSectionName(sec)
		if sec == "" || seenInFile[sec] {
			return
		}
		seenInFile[sec] = true
		claims = append(claims, SectionClaim{
			Section: sec,
			File:    filename,
			Line:    lineNum,
		})
	}

	inMultiDouble := false
	inMultiSingle := false
	currentSection := ""

	for idx, rawLine := range lines {
		lineNum := idx + 1
		cleaned, nextMultiDouble, nextMultiSingle := stripTOMLCommentsAndMultiline(rawLine, inMultiDouble, inMultiSingle)
		inMultiDouble = nextMultiDouble
		inMultiSingle = nextMultiSingle

		trimmed := strings.TrimSpace(cleaned)
		if trimmed == "" {
			continue
		}

		// Explicit table header: [section] or [[array_of_tables]]
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			header := trimmed
			if strings.HasPrefix(header, "[[") && strings.HasSuffix(header, "]]") {
				header = strings.TrimSpace(header[2 : len(header)-2])
			} else {
				header = strings.TrimSpace(header[1 : len(header)-1])
			}
			norm := normalizeSectionName(header)
			if norm != "" {
				currentSection = norm
				addClaim(norm, lineNum)
			}
			continue
		}

		// Key-value assignment: check for dotted sub-table keys or inline table `{ ... }`
		if eqIdx := findUnquotedEquals(trimmed); eqIdx > 0 {
			rawKey := strings.TrimSpace(trimmed[:eqIdx])
			rawVal := strings.TrimSpace(trimmed[eqIdx+1:])
			keyParts := splitTOMLDottedKey(rawKey)

			// Dotted key inside currentSection or root (e.g. `ai.endpoint = "..."` inside `[critique]`)
			if len(keyParts) > 1 {
				subPath := strings.Join(keyParts[:len(keyParts)-1], ".")
				fullSec := subPath
				if currentSection != "" {
					fullSec = currentSection + "." + subPath
				}
				addClaim(fullSec, lineNum)
			}

			// Inline table assignment (e.g. `ai = { enabled = true }` inside `[critique]`)
			if strings.HasPrefix(rawVal, "{") {
				keyPath := strings.Join(keyParts, ".")
				fullSec := keyPath
				if currentSection != "" {
					fullSec = currentSection + "." + keyPath
				}
				addClaim(fullSec, lineNum)
			}
		}
	}

	return claims, nil
}

func normalizeSectionName(raw string) string {
	parts := splitTOMLDottedKey(raw)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ".")
}

func splitTOMLDottedKey(raw string) []string {
	var parts []string
	var buf strings.Builder
	inDouble := false
	inSingle := false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '.' && !inDouble && !inSingle {
			part := strings.TrimSpace(buf.String())
			if part != "" {
				parts = append(parts, part)
			}
			buf.Reset()
			continue
		}
		buf.WriteByte(ch)
	}
	if last := strings.TrimSpace(buf.String()); last != "" {
		parts = append(parts, last)
	}
	return parts
}

func findUnquotedEquals(s string) int {
	inDouble := false
	inSingle := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' && !inSingle {
			inDouble = !inDouble
		} else if ch == '\'' && !inDouble {
			inSingle = !inSingle
		} else if ch == '=' && !inDouble && !inSingle {
			return i
		}
	}
	return -1
}

func stripTOMLCommentsAndMultiline(line string, inMultiDouble, inMultiSingle bool) (string, bool, bool) {
	var out strings.Builder
	inDouble := false
	inSingle := false

	i := 0
	for i < len(line) {
		if inMultiDouble {
			if strings.HasPrefix(line[i:], `"""`) {
				inMultiDouble = false
				i += 3
			} else {
				i++
			}
			continue
		}
		if inMultiSingle {
			if strings.HasPrefix(line[i:], `'''`) {
				inMultiSingle = false
				i += 3
			} else {
				i++
			}
			continue
		}

		if !inSingle && strings.HasPrefix(line[i:], `"""`) {
			inMultiDouble = true
			i += 3
			continue
		}
		if !inDouble && strings.HasPrefix(line[i:], `'''`) {
			inMultiSingle = true
			i += 3
			continue
		}

		ch := line[i]
		if ch == '\\' && inDouble && i+1 < len(line) {
			out.WriteByte(ch)
			out.WriteByte(line[i+1])
			i += 2
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			out.WriteByte(ch)
			i++
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			out.WriteByte(ch)
			i++
			continue
		}
		if ch == '#' && !inDouble && !inSingle {
			break
		}
		out.WriteByte(ch)
		i++
	}

	return out.String(), inMultiDouble, inMultiSingle
}

func formatFileLoadError(path string, err error) error {
	var decodeErr *toml.DecodeError
	if errors.As(err, &decodeErr) {
		row, col := decodeErr.Position()
		return fmt.Errorf("configuration syntax error in %s (line %d, column %d):\n  %s\nHint: Verify TOML syntax (check for unclosed quotes, invalid types, or missing '=').", path, row, col, decodeErr.Error())
	}
	return fmt.Errorf("failed to load configuration from %s:\n%w", path, err)
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
	cfg.Critique.AI.Endpoint = os.ExpandEnv(cfg.Critique.AI.Endpoint)
	cfg.Critique.AI.SystemPromptPath = os.ExpandEnv(cfg.Critique.AI.SystemPromptPath)
	cfg.Critique.AI.TLS.CAFile = os.ExpandEnv(cfg.Critique.AI.TLS.CAFile)

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

	authUser := strings.TrimSpace(os.ExpandEnv(cfg.Critique.Auth.DefaultUser))
	if authUser == "" || authUser == "$USER" {
		authUser = reviewer
	}
	cfg.Critique.Auth.DefaultUser = authUser

	if cfg.Critique.Auth.Mode == "" {
		cfg.Critique.Auth.Mode = "local"
	}
	if cfg.Critique.Auth.HeaderName == "" {
		cfg.Critique.Auth.HeaderName = "X-Forwarded-User"
	}
}
