package config

import (
	"fmt"
	"strings"
)

// ResolvedTheme contains fully evaluated hex/rgba colors and Chroma syntax style.
type ResolvedTheme struct {
	Name           string
	SyntaxStyle    string
	BGCanvas       string
	BGSurface      string
	BGCard         string
	Border         string
	TextPrimary    string
	TextMuted      string
	Accent         string
	DiffAddBG      string
	DiffAddBorder  string
	DiffDelBG      string
	DiffDelBorder  string
	DiffModLeftBG  string
	DiffModRightBG string
}

// PresetThemes defines canonical color values for each built-in preset.
var PresetThemes = map[string]ResolvedTheme{
	"catppuccin-mocha": {
		Name:           "catppuccin-mocha",
		SyntaxStyle:    "catppuccin-mocha",
		BGCanvas:       "#1e1e2e",
		BGSurface:      "#181825",
		BGCard:         "#11111b",
		Border:         "#313244",
		TextPrimary:    "#cdd6f4",
		TextMuted:      "#a6adc8",
		Accent:         "#89b4fa",
		DiffAddBG:      "rgba(166, 227, 161, 0.20)",
		DiffAddBorder:  "#a6e3a1",
		DiffDelBG:      "rgba(243, 139, 168, 0.20)",
		DiffDelBorder:  "#f38ba8",
		DiffModLeftBG:  "rgba(243, 139, 168, 0.12)",
		DiffModRightBG: "rgba(166, 227, 161, 0.12)",
	},
	"catppuccin-macchiato": {
		Name:           "catppuccin-macchiato",
		SyntaxStyle:    "catppuccin-macchiato",
		BGCanvas:       "#24273a",
		BGSurface:      "#1e2030",
		BGCard:         "#181926",
		Border:         "#363a4f",
		TextPrimary:    "#cad3f5",
		TextMuted:      "#a5adcb",
		Accent:         "#8aadf4",
		DiffAddBG:      "rgba(166, 218, 149, 0.20)",
		DiffAddBorder:  "#a6da95",
		DiffDelBG:      "rgba(237, 135, 150, 0.20)",
		DiffDelBorder:  "#ed8796",
		DiffModLeftBG:  "rgba(237, 135, 150, 0.12)",
		DiffModRightBG: "rgba(166, 218, 149, 0.12)",
	},
	"catppuccin-frappe": {
		Name:           "catppuccin-frappe",
		SyntaxStyle:    "catppuccin-frappe",
		BGCanvas:       "#303446",
		BGSurface:      "#292c3c",
		BGCard:         "#232634",
		Border:         "#414559",
		TextPrimary:    "#c6d0f5",
		TextMuted:      "#a5b4fc",
		Accent:         "#8caaee",
		DiffAddBG:      "rgba(166, 209, 137, 0.20)",
		DiffAddBorder:  "#a6d189",
		DiffDelBG:      "rgba(231, 130, 132, 0.20)",
		DiffDelBorder:  "#e78284",
		DiffModLeftBG:  "rgba(231, 130, 132, 0.12)",
		DiffModRightBG: "rgba(166, 209, 137, 0.12)",
	},
	"catppuccin-latte": {
		Name:           "catppuccin-latte",
		SyntaxStyle:    "catppuccin-latte",
		BGCanvas:       "#eff1f5",
		BGSurface:      "#e6e9ef",
		BGCard:         "#dce0e8",
		Border:         "#ccd0da",
		TextPrimary:    "#4c4f69",
		TextMuted:      "#6c6f85",
		Accent:         "#1e66f5",
		DiffAddBG:      "rgba(64, 160, 43, 0.15)",
		DiffAddBorder:  "#40a02b",
		DiffDelBG:      "rgba(210, 15, 57, 0.15)",
		DiffDelBorder:  "#d20f39",
		DiffModLeftBG:  "rgba(210, 15, 57, 0.08)",
		DiffModRightBG: "rgba(64, 160, 43, 0.08)",
	},
	"tokyo-night": {
		Name:           "tokyo-night",
		SyntaxStyle:    "tokyonight-night",
		BGCanvas:       "#1a1b26",
		BGSurface:      "#16161e",
		BGCard:         "#13141c",
		Border:         "#292e42",
		TextPrimary:    "#c0caf5",
		TextMuted:      "#787c99",
		Accent:         "#7aa2f7",
		DiffAddBG:      "rgba(115, 218, 202, 0.20)",
		DiffAddBorder:  "#73daca",
		DiffDelBG:      "rgba(247, 118, 142, 0.20)",
		DiffDelBorder:  "#f7768e",
		DiffModLeftBG:  "rgba(247, 118, 142, 0.12)",
		DiffModRightBG: "rgba(115, 218, 202, 0.12)",
	},
	"dracula": {
		Name:           "dracula",
		SyntaxStyle:    "dracula",
		BGCanvas:       "#282a36",
		BGSurface:      "#21222c",
		BGCard:         "#191a21",
		Border:         "#44475a",
		TextPrimary:    "#f8f8f2",
		TextMuted:      "#6272a4",
		Accent:         "#bd93f9",
		DiffAddBG:      "rgba(80, 250, 123, 0.20)",
		DiffAddBorder:  "#50fa7b",
		DiffDelBG:      "rgba(255, 85, 85, 0.20)",
		DiffDelBorder:  "#ff5555",
		DiffModLeftBG:  "rgba(255, 85, 85, 0.12)",
		DiffModRightBG: "rgba(80, 250, 123, 0.12)",
	},
	"nord": {
		Name:           "nord",
		SyntaxStyle:    "nord",
		BGCanvas:       "#2e3440",
		BGSurface:      "#242933",
		BGCard:         "#1e222a",
		Border:         "#3b4252",
		TextPrimary:    "#eceff4",
		TextMuted:      "#d8dee9",
		Accent:         "#88c0d0",
		DiffAddBG:      "rgba(163, 190, 140, 0.20)",
		DiffAddBorder:  "#a3be8c",
		DiffDelBG:      "rgba(191, 97, 106, 0.20)",
		DiffDelBorder:  "#bf616a",
		DiffModLeftBG:  "rgba(191, 97, 106, 0.12)",
		DiffModRightBG: "rgba(163, 190, 140, 0.12)",
	},
	"github-dark": {
		Name:           "github-dark",
		SyntaxStyle:    "github-dark",
		BGCanvas:       "#0d1117",
		BGSurface:      "#161b22",
		BGCard:         "#090d16",
		Border:         "#30363d",
		TextPrimary:    "#e6edf3",
		TextMuted:      "#7d8590",
		Accent:         "#2f81f7",
		DiffAddBG:      "rgba(46, 160, 67, 0.20)",
		DiffAddBorder:  "#2ea043",
		DiffDelBG:      "rgba(248, 81, 73, 0.20)",
		DiffDelBorder:  "#f85149",
		DiffModLeftBG:  "rgba(248, 81, 73, 0.12)",
		DiffModRightBG: "rgba(46, 160, 67, 0.12)",
	},
}

// ResolveTheme resolves the active ThemeConfig into fully merged colors and syntax style.
func ResolveTheme(tc ThemeConfig) *ResolvedTheme {
	presetKey := strings.ToLower(strings.TrimSpace(tc.Preset))
	if presetKey == "" {
		presetKey = "catppuccin-mocha"
	}

	base, ok := PresetThemes[presetKey]
	if !ok {
		// Fallback to catppuccin-mocha if preset not found
		base = PresetThemes["catppuccin-mocha"]
		base.Name = presetKey
	}

	res := base

	// Override syntax style if explicitly specified
	if tc.SyntaxStyle != "" {
		res.SyntaxStyle = strings.TrimSpace(tc.SyntaxStyle)
	}

	// Apply individual color overrides
	c := tc.Colors
	if c.BGCanvas != "" {
		res.BGCanvas = c.BGCanvas
	}
	if c.BGSurface != "" {
		res.BGSurface = c.BGSurface
	}
	if c.BGCard != "" {
		res.BGCard = c.BGCard
	}
	if c.Border != "" {
		res.Border = c.Border
	}
	if c.TextPrimary != "" {
		res.TextPrimary = c.TextPrimary
	}
	if c.TextMuted != "" {
		res.TextMuted = c.TextMuted
	}
	if c.Accent != "" {
		res.Accent = c.Accent
	}
	if c.DiffAddBG != "" {
		res.DiffAddBG = c.DiffAddBG
	}
	if c.DiffAddBorder != "" {
		res.DiffAddBorder = c.DiffAddBorder
	}
	if c.DiffDelBG != "" {
		res.DiffDelBG = c.DiffDelBG
	}
	if c.DiffDelBorder != "" {
		res.DiffDelBorder = c.DiffDelBorder
	}
	if c.DiffModLeftBG != "" {
		res.DiffModLeftBG = c.DiffModLeftBG
	}
	if c.DiffModRightBG != "" {
		res.DiffModRightBG = c.DiffModRightBG
	}

	return &res
}

// ToCSSVariables serializes the resolved theme into CSS custom property declarations.
func (r *ResolvedTheme) ToCSSVariables() string {
	return fmt.Sprintf(`:root {
  --critique-bg-canvas: %s;
  --critique-bg-surface: %s;
  --critique-bg-card: %s;
  --critique-border: %s;
  --critique-text: %s;
  --critique-text-muted: %s;
  --critique-accent: %s;
  --critique-diff-add-bg: %s;
  --critique-diff-add-border: %s;
  --critique-diff-del-bg: %s;
  --critique-diff-del-border: %s;
  --critique-diff-mod-left-bg: %s;
  --critique-diff-mod-right-bg: %s;
}`,
		r.BGCanvas,
		r.BGSurface,
		r.BGCard,
		r.Border,
		r.TextPrimary,
		r.TextMuted,
		r.Accent,
		r.DiffAddBG,
		r.DiffAddBorder,
		r.DiffDelBG,
		r.DiffDelBorder,
		r.DiffModLeftBG,
		r.DiffModRightBG,
	)
}
