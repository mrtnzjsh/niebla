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
	t.Setenv("CITC_CONFIG", configPath)

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
	configPath := filepath.Join(tempDir, "citc-toolchain", "config.toml")
	t.Setenv("CITC_CONFIG", configPath)

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
