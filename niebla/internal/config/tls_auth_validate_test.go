package config

import (
	"strings"
	"testing"
)

func TestValidateServerTLSConfig(t *testing.T) {
	// 1. Default disabled -> valid
	cfg := DefaultConfig()
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected default config to be valid: %v", err)
	}

	// 2. TLS enabled without cert_file -> error
	cfg.Server.TLS.Enabled = true
	cfg.Server.TLS.CertFile = ""
	cfg.Server.TLS.KeyFile = "/path/to/key.pem"
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "cert_file cannot be empty") {
		t.Errorf("expected error for missing cert_file, got: %v", err)
	}

	// 3. TLS enabled without key_file -> error
	cfg.Server.TLS.CertFile = "/path/to/cert.pem"
	cfg.Server.TLS.KeyFile = "   "
	err = Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "key_file cannot be empty") {
		t.Errorf("expected error for missing key_file, got: %v", err)
	}

	// 4. TLS enabled with valid paths -> valid
	cfg.Server.TLS.CertFile = "/path/to/cert.pem"
	cfg.Server.TLS.KeyFile = "/path/to/key.pem"
	if err := Validate(cfg); err != nil {
		t.Errorf("expected valid TLS config, got: %v", err)
	}
}

func TestValidateServerAuthTokens(t *testing.T) {
	cfg := DefaultConfig()

	// 1. Valid tokens
	cfg.Server.AuthTokens = []string{"token-alpha", "token-beta-123"}
	if err := Validate(cfg); err != nil {
		t.Fatalf("expected valid auth tokens to pass, got: %v", err)
	}

	// 2. Empty or whitespace token
	cfg.Server.AuthTokens = []string{"valid-token", "   "}
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "auth token cannot be empty or whitespace") {
		t.Errorf("expected error for whitespace auth token, got: %v", err)
	}
}
