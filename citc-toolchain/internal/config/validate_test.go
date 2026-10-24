package config

import (
	"strings"
	"testing"
)

func TestValidateDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if err := Validate(cfg); err != nil {
		t.Fatalf("DefaultConfig should be valid, got: %v", err)
	}
}

func TestValidatePortRanges(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Port = 70000
	cfg.Critique.Port = -5

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("expected validation error for invalid ports, got nil")
	}

	verrs, ok := err.(ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors type, got %T", err)
	}

	if len(verrs) != 2 {
		t.Errorf("expected 2 validation errors, got %d: %v", len(verrs), verrs)
	}
}

func TestValidatePortCollision(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Critique.Port = 8080
	cfg.Critique.ProxyPort = 8080

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("expected validation error for colliding ports, got nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "critique.proxy_port conflicts with critique.port") {
		t.Errorf("expected port conflict message, got: %s", errStr)
	}
}

func TestValidateThemePresetWithTypoSuggestion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Critique.Theme.Preset = "cattppuccin"

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("expected validation error for unknown preset, got nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "unknown theme preset \"cattppuccin\"") {
		t.Errorf("expected unknown preset error, got: %s", errStr)
	}
	if !strings.Contains(errStr, "Did you mean \"catppuccin-") {
		t.Errorf("expected typo hint suggestion, got: %s", errStr)
	}
}

func TestValidateThemeColors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Critique.Theme.Colors.BGCanvas = "invalid-color-value"
	cfg.Critique.Theme.Colors.Accent = "#89b4fa"                  // Valid
	cfg.Critique.Theme.Colors.DiffAddBG = "rgba(166, 227, 161, 0.20)" // Valid

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("expected validation error for invalid color, got nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "invalid color format \"invalid-color-value\"") {
		t.Errorf("expected invalid color format error, got: %s", errStr)
	}
}

func TestValidateSyntaxStyle(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Critique.Theme.SyntaxStyle = "non-existent-chroma-style-xyz"

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("expected validation error for invalid Chroma syntax style, got nil")
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "unrecognized Chroma syntax style") {
		t.Errorf("expected unrecognized Chroma style error, got: %s", errStr)
	}
}
