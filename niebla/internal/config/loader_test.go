package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadBytesValidOverride(t *testing.T) {
	customTOML := `
version = 1

[critique]
port = 9090
proxy_port = 7332

[critique.theme]
preset = "tokyo-night"
`
	cfg, err := LoadBytes([]byte(customTOML))
	if err != nil {
		t.Fatalf("expected valid config to load, got: %v", err)
	}

	if cfg.Critique.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Critique.Port)
	}
	if cfg.Critique.ProxyPort != 7332 {
		t.Errorf("expected proxy_port 7332, got %d", cfg.Critique.ProxyPort)
	}
	if cfg.Critique.Theme.Preset != "tokyo-night" {
		t.Errorf("expected preset tokyo-night, got %s", cfg.Critique.Theme.Preset)
	}
	// Verify unmentioned fields retain baseline defaults
	if cfg.Server.Port != 50051 {
		t.Errorf("expected baseline server port 50051, got %d", cfg.Server.Port)
	}
}

func TestLoadBytesInvalidTOMLSyntaxFailsFast(t *testing.T) {
	badSyntaxTOML := `
version = 1
[server
port = 50051
`
	_, err := LoadBytes([]byte(badSyntaxTOML))
	if err == nil {
		t.Fatalf("expected TOML syntax error to fail fast, got nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "syntax error") && !strings.Contains(errStr, "unmarshal") && !strings.Contains(errStr, "missing") {
		t.Logf("syntax error details: %s", errStr)
	}
}

func TestLoadBytesSemanticValidationErrorFailsFast(t *testing.T) {
	badConfigTOML := `
version = 1

[critique]
port = 8080
proxy_port = 8080 # Port conflict!
`
	_, err := LoadBytes([]byte(badConfigTOML))
	if err == nil {
		t.Fatalf("expected semantic validation error to fail fast, got nil")
	}

	verrs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("expected error of type ValidationErrors, got %T: %v", err, err)
	}

	if len(verrs) == 0 {
		t.Fatalf("expected at least 1 validation error, got 0")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "critique.proxy_port conflicts with critique.port") {
		t.Errorf("expected port collision message, got: %s", errStr)
	}
}

func TestLoadFromPathFirstRunInit(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "niebla", "config.toml")

	// Ensure file doesn't exist yet
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected config file to not exist initially")
	}

	// Loading should initialize the file from BaseConfigBytes
	cfg, err := LoadFromPath(configPath)
	if err != nil {
		t.Fatalf("failed to initialize and load config from path: %v", err)
	}

	if cfg == nil {
		t.Fatalf("expected loaded config struct, got nil")
	}

	// Verify file was written to disk
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("expected file to exist after LoadFromPath: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("expected non-empty config file created")
	}
}

func TestLoadFromPathSyntaxErrorFailsFastWithDescriptiveHint(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")

	badContent := `version = 1
[critique]
port = "not a number"
`
	if err := os.WriteFile(configPath, []byte(badContent), 0644); err != nil {
		t.Fatalf("failed to write bad config file: %v", err)
	}

	_, err := LoadFromPath(configPath)
	if err == nil {
		t.Fatalf("expected failure for invalid type in TOML, got nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "configuration syntax error") {
		t.Errorf("expected descriptive syntax error message, got: %s", errStr)
	}
	if !strings.Contains(errStr, "Hint:") {
		t.Errorf("expected actionable Hint in error message, got: %s", errStr)
	}
}

func TestEnvironmentVariableExpansion(t *testing.T) {
	t.Setenv("TEST_REVIEWER", "test_reviewer")
	t.Setenv("TEST_BLOB_PATH", "/tmp/test_blobs")

	tomlData := `
version = 1

[server]
blob_path = "$TEST_BLOB_PATH"

[critique]
default_reviewer = "$TEST_REVIEWER"
`
	cfg, err := LoadBytes([]byte(tomlData))
	if err != nil {
		t.Fatalf("failed to load config with env vars: %v", err)
	}

	if cfg.Server.BlobPath != "/tmp/test_blobs" {
		t.Errorf("expected expanded blob_path '/tmp/test_blobs', got %q", cfg.Server.BlobPath)
	}
	if cfg.Critique.DefaultReviewer != "test_reviewer" {
		t.Errorf("expected expanded default_reviewer 'test_reviewer', got %q", cfg.Critique.DefaultReviewer)
	}
}

func TestLoadFromDir_CombinesDisjointSubsections(t *testing.T) {
	dir := t.TempDir()

	mainTOML := `
version = 1

[core]
server_addr = "127.0.0.1:50051"

[critique]
port = 8088
proxy_port = 7331
`
	themeTOML := `
[critique.theme]
preset = "dracula"
syntax_style = "dracula"
`
	aiTOML := `
[critique.ai]
enabled = true
endpoint = "http://127.0.0.1:11434/v1"
model = "qwen2.5-coder"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(mainTOML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "theme.toml"), []byte(themeTOML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ai.toml"), []byte(aiTOML), 0644); err != nil {
		t.Fatal(err)
	}

	// Loading via config.toml path should automatically discover and combine sibling *.toml files
	cfg, err := LoadFromPath(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatalf("expected disjoint multi-file TOMLs to combine cleanly, got: %v", err)
	}

	if cfg.Critique.Port != 8088 {
		t.Errorf("expected critique.port 8088 from config.toml, got %d", cfg.Critique.Port)
	}
	if cfg.Critique.Theme.Preset != "dracula" {
		t.Errorf("expected critique.theme.preset 'dracula' from theme.toml, got %q", cfg.Critique.Theme.Preset)
	}
	if !cfg.Critique.AI.IsConfigured() || cfg.Critique.AI.Endpoint != "http://127.0.0.1:11434/v1" {
		t.Errorf("expected critique.ai from ai.toml to be configured, got %+v", cfg.Critique.AI)
	}
}

func TestLoadFromDir_RejectsDuplicateHeaderAcrossFiles(t *testing.T) {
	dir := t.TempDir()

	// Both config.toml and ai.toml define [critique.ai] (even with different attributes)
	mainTOML := `
version = 1

[critique]
port = 8080

[critique.ai]
enabled = true
`
	aiTOML := `
[critique.ai]
endpoint = "http://127.0.0.1:11434/v1"
model = "qwen2.5-coder"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(mainTOML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ai.toml"), []byte(aiTOML), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFromPath(filepath.Join(dir, "config.toml"))
	if err == nil {
		t.Fatalf("expected error when [critique.ai] is defined in both config.toml and ai.toml, got nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "duplicate configuration section [critique.ai]") {
		t.Errorf("expected duplicate section error message, got:\n%s", errStr)
	}
	if !strings.Contains(errStr, "config.toml") || !strings.Contains(errStr, "ai.toml") {
		t.Errorf("expected error to name both conflicting files (config.toml and ai.toml), got:\n%s", errStr)
	}
}

func TestLoadFromDir_RejectsInlineSubtableConflict(t *testing.T) {
	dir := t.TempDir()

	// config.toml defines inline `ai = { ... }` under [critique], while ai.toml defines [critique.ai]
	mainTOML := `
version = 1

[critique]
port = 8080
ai = { enabled = true }
`
	aiTOML := `
[critique.ai]
endpoint = "http://127.0.0.1:11434/v1"
model = "qwen2.5-coder"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(mainTOML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ai.toml"), []byte(aiTOML), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFromDir(dir)
	if err == nil {
		t.Fatalf("expected duplicate section conflict for inline table vs [critique.ai] header, got nil")
	}
	if !strings.Contains(err.Error(), "duplicate configuration section [critique.ai]") {
		t.Errorf("expected duplicate section [critique.ai] error, got: %v", err)
	}
}

