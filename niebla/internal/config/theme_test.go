package config

import (
	"strings"
	"testing"
)

func TestResolvePresetThemes(t *testing.T) {
	for _, preset := range SupportedPresets {
		if preset == "custom" {
			continue
		}
		tc := ThemeConfig{Preset: preset}
		resolved := ResolveTheme(tc)
		if resolved == nil {
			t.Fatalf("expected resolved theme for preset %q, got nil", preset)
		}
		if resolved.BGCanvas == "" || resolved.Accent == "" || resolved.SyntaxStyle == "" {
			t.Errorf("preset %q missing essential fields: %+v", preset, resolved)
		}
	}
}

func TestResolveThemeDefaultFallback(t *testing.T) {
	tc := ThemeConfig{} // Empty preset
	resolved := ResolveTheme(tc)
	if resolved.Name != "catppuccin-mocha" {
		t.Errorf("expected default theme catppuccin-mocha, got %q", resolved.Name)
	}
	if resolved.BGCanvas != "#1e1e2e" {
		t.Errorf("expected mocha bg_canvas #1e1e2e, got %s", resolved.BGCanvas)
	}
}

func TestResolveThemeOverrides(t *testing.T) {
	tc := ThemeConfig{
		Preset: "tokyo-night",
		Colors: ColorsConfig{
			Accent:    "#ff00ff",
			DiffAddBG: "rgba(0, 255, 0, 0.5)",
		},
		SyntaxStyle: "dracula",
	}

	resolved := ResolveTheme(tc)
	if resolved.Name != "tokyo-night" {
		t.Errorf("expected name tokyo-night, got %s", resolved.Name)
	}
	if resolved.Accent != "#ff00ff" {
		t.Errorf("expected accent override #ff00ff, got %s", resolved.Accent)
	}
	if resolved.DiffAddBG != "rgba(0, 255, 0, 0.5)" {
		t.Errorf("expected diff_add_bg override, got %s", resolved.DiffAddBG)
	}
	// Non-overridden color should remain from tokyo-night
	if resolved.BGCanvas != "#1a1b26" {
		t.Errorf("expected tokyo-night BGCanvas #1a1b26, got %s", resolved.BGCanvas)
	}
	// Syntax style override
	if resolved.SyntaxStyle != "dracula" {
		t.Errorf("expected syntax style override 'dracula', got %s", resolved.SyntaxStyle)
	}
}

func TestToCSSVariables(t *testing.T) {
	resolved := ResolveTheme(ThemeConfig{Preset: "catppuccin-mocha"})
	css := resolved.ToCSSVariables()

	expectedSubstrings := []string{
		":root {",
		"--critique-bg-canvas: #1e1e2e;",
		"--critique-accent: #89b4fa;",
		"--critique-diff-add-border: #a6e3a1;",
		"--critique-diff-del-border: #f38ba8;",
	}

	for _, sub := range expectedSubstrings {
		if !strings.Contains(css, sub) {
			t.Errorf("expected CSS variables to contain %q, got:\n%s", sub, css)
		}
	}
}
