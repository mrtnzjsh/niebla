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

func TestValidateVersion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Version = 0

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("expected validation error for version 0, got nil")
	}
	if !strings.Contains(err.Error(), "version must be at least 1") {
		t.Errorf("expected version error, got: %v", err)
	}
}

func TestValidateServerAddr(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Core.ServerAddr = "invalid-address-without-port"

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("expected validation error for invalid ServerAddr, got nil")
	}
	if !strings.Contains(err.Error(), "invalid server address format") {
		t.Errorf("expected server address format error, got: %v", err)
	}
}

func TestValidateInterServicePortCollisions(t *testing.T) {
	// Server vs Critique Port
	cfg1 := DefaultConfig()
	cfg1.Server.Port = 8080
	cfg1.Critique.Port = 8080

	err1 := Validate(cfg1)
	if err1 == nil || !strings.Contains(err1.Error(), "critique.port conflicts with server.port") {
		t.Errorf("expected critique.port conflicts with server.port, got: %v", err1)
	}

	// Server vs Critique ProxyPort
	cfg2 := DefaultConfig()
	cfg2.Server.Port = 7331
	cfg2.Critique.ProxyPort = 7331

	err2 := Validate(cfg2)
	if err2 == nil || !strings.Contains(err2.Error(), "critique.proxy_port conflicts with server.port") {
		t.Errorf("expected critique.proxy_port conflicts with server.port, got: %v", err2)
	}
}

func TestValidateDBURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.DBURL = "mysql://root@localhost/db"

	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "unsupported PostgreSQL connection string format") {
		t.Errorf("expected db_url format error, got: %v", err)
	}
}

func TestValidateFSMountBase(t *testing.T) {
	cfg := DefaultConfig()
	cfg.FS.MountBase = "relative/path/not/absolute"

	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "mount_base must be an absolute path") {
		t.Errorf("expected absolute path error, got: %v", err)
	}
}

func TestValidateDefaultReviewer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Critique.DefaultReviewer = "bad user with spaces"

	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "default_reviewer cannot contain whitespace") {
		t.Errorf("expected whitespace error for reviewer, got: %v", err)
	}
}

func TestValidateAuthMode(t *testing.T) {
	cfg := DefaultConfig()

	// Valid modes
	for _, m := range []string{"local", "headers", "tailscale"} {
		cfg.Critique.Auth.Mode = m
		if err := Validate(cfg); err != nil {
			t.Errorf("expected mode %q to be valid, got: %v", m, err)
		}
	}

	// Invalid mode
	cfg.Critique.Auth.Mode = "invalid_mode"
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown auth mode") {
		t.Errorf("expected unknown auth mode error, got: %v", err)
	}
}

func TestValidateRequiredApprovals(t *testing.T) {
	cfg := DefaultConfig()

	// Negative value should fail validation
	neg := -1
	cfg.Critique.RequiredApprovals = &neg
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "required_approvals cannot be negative") {
		t.Errorf("expected negative required_approvals error, got: %v", err)
	}

	// Zero and positive values should pass
	zero := 0
	cfg.Critique.RequiredApprovals = &zero
	if err := Validate(cfg); err != nil {
		t.Errorf("expected 0 required_approvals to be valid, got: %v", err)
	}

	two := 2
	cfg.Critique.RequiredApprovals = &two
	if err := Validate(cfg); err != nil {
		t.Errorf("expected 2 required_approvals to be valid, got: %v", err)
	}
}

func TestValidateGroupsConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Invalid mode
	cfg.Critique.Groups.Mode = "ldap_custom"
	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "unknown groups mode") {
		t.Errorf("expected unknown groups mode error, got: %v", err)
	}

	// API mode with empty URL
	cfg.Critique.Groups.Mode = "api"
	cfg.Critique.Groups.API.URL = ""
	err = Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "groups API URL cannot be empty") {
		t.Errorf("expected empty API URL error, got: %v", err)
	}

	// Valid API mode
	cfg.Critique.Groups.API.URL = "https://idp.example.com/groups"
	if err := Validate(cfg); err != nil {
		t.Errorf("expected valid API mode, got: %v", err)
	}
}

func TestConfigDefaultsAndGetters(t *testing.T) {
	cfg := DefaultConfig()

	// In local mode, default required approvals should be 0
	cfg.Critique.Auth.Mode = "local"
	cfg.Critique.RequiredApprovals = nil
	if val := cfg.Critique.GetRequiredApprovals(); val != 0 {
		t.Errorf("expected local mode to default to 0 required approvals, got %d", val)
	}

	// In tailscale mode, default required approvals should be 0
	cfg.Critique.Auth.Mode = "tailscale"
	if val := cfg.Critique.GetRequiredApprovals(); val != 0 {
		t.Errorf("expected tailscale mode to default to 0 required approvals, got %d", val)
	}

	// In headers mode, default required approvals should be 1
	cfg.Critique.Auth.Mode = "headers"
	if val := cfg.Critique.GetRequiredApprovals(); val != 1 {
		t.Errorf("expected headers mode to default to 1 required approvals, got %d", val)
	}

	// Explicit override takes precedence
	three := 3
	cfg.Critique.RequiredApprovals = &three
	if val := cfg.Critique.GetRequiredApprovals(); val != 3 {
		t.Errorf("expected explicit required approvals 3, got %d", val)
	}

	// AllowSubmitWithUnresolvedComments defaults to false
	if val := cfg.Critique.GetAllowSubmitWithUnresolvedComments(); val != false {
		t.Errorf("expected default allow submit with unresolved comments to be false, got %t", val)
	}

	allow := true
	cfg.Critique.AllowSubmitWithUnresolvedComments = &allow
	if val := cfg.Critique.GetAllowSubmitWithUnresolvedComments(); val != true {
		t.Errorf("expected allow submit with unresolved comments to be true, got %t", val)
	}
}



