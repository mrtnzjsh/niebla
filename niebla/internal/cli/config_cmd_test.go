package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigPathCmd(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	t.Setenv("NIEBLA_CONFIG", configPath)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "path"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config path failed: %v", err)
	}

	out := strings.TrimSpace(buf.String())
	if out != configPath {
		t.Errorf("expected path %q, got %q", configPath, out)
	}
}

func TestConfigValidateCmdSuccess(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	validTOML := `version = 1
[critique]
port = 8080
proxy_port = 7331
`
	if err := os.WriteFile(configPath, []byte(validTOML), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "validate", configPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config validate failed on valid config: %v", err)
	}

	if !strings.Contains(buf.String(), "✓ Configuration is valid") {
		t.Errorf("expected success message, got:\n%s", buf.String())
	}
}

func TestConfigValidateCmdFailsFast(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "invalid_config.toml")
	invalidTOML := `version = 1
[critique]
port = 9000
proxy_port = 9000 # Conflict!
`
	if err := os.WriteFile(configPath, []byte(invalidTOML), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	rootCmd := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetArgs([]string{"config", "validate", configPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatalf("expected validation command to fail fast with error, got nil")
	}

	combined := outBuf.String() + "\n" + errBuf.String()
	if !strings.Contains(combined, "critique.proxy_port conflicts with critique.port") {
		t.Errorf("expected conflict message in output, got:\n%s", combined)
	}
}

func TestConfigInitCmd(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "niebla", "config.toml")
	t.Setenv("NIEBLA_CONFIG", configPath)

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "init"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("config init failed: %v", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatalf("expected config file to be created at %s", configPath)
	}

	if !strings.Contains(buf.String(), "Initialized configuration file") {
		t.Errorf("expected init success message, got:\n%s", buf.String())
	}
}

func TestConfigValidateCmd_MultiFileAndDuplicateSection(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.toml")
	aiPath := filepath.Join(tempDir, "ai.toml")

	mainTOML := `version = 1
[critique]
port = 8080
proxy_port = 7331
`
	aiTOML := `[critique.ai]
enabled = true
endpoint = "http://127.0.0.1:11434/v1"
model = "qwen2.5-coder"
`
	if err := os.WriteFile(configPath, []byte(mainTOML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(aiPath, []byte(aiTOML), 0644); err != nil {
		t.Fatal(err)
	}

	// 1. Disjoint headers across config.toml and ai.toml pass validation
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"config", "validate", configPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected multi-file config validate to succeed, got: %v\n%s", err, buf.String())
	}
	if !strings.Contains(buf.String(), "Combined files") {
		t.Errorf("expected 'Combined files' summary in validate output, got:\n%s", buf.String())
	}

	// 2. Adding duplicate [critique] header into ai.toml fails fast
	dupAITOML := `[critique]
default_reviewer = "alice"

[critique.ai]
enabled = true
endpoint = "http://127.0.0.1:11434/v1"
model = "qwen2.5-coder"
`
	if err := os.WriteFile(aiPath, []byte(dupAITOML), 0644); err != nil {
		t.Fatal(err)
	}

	rootCmd2 := NewRootCmd()
	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)
	rootCmd2.SetOut(outBuf)
	rootCmd2.SetErr(errBuf)
	rootCmd2.SetArgs([]string{"config", "validate", configPath})
	if err := rootCmd2.Execute(); err == nil {
		t.Fatalf("expected config validate to fail on duplicate [critique] header across files")
	}
	combined := outBuf.String() + "\n" + errBuf.String()
	if !strings.Contains(combined, "duplicate configuration section [critique]") {
		t.Errorf("expected duplicate section error in output, got:\n%s", combined)
	}
}

