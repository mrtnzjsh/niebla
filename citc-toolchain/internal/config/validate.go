package config

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2/styles"
)

// SupportedPresets lists all officially built-in theme presets.
var SupportedPresets = []string{
	"catppuccin-mocha",
	"catppuccin-latte",
	"catppuccin-macchiato",
	"catppuccin-frappe",
	"tokyo-night",
	"dracula",
	"nord",
	"github-dark",
	"custom",
}

var (
	hexColorRegex = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	cssColorRegex = regexp.MustCompile(`^(rgba?|hsla?)\(\s*[\d\.\%]+[\s,]+[\d\.\%]+[\s,]+[\d\.\%]+([\s,\/]+[\d\.\%]+)?\s*\)$`)
)

// ValidationError represents a specific configuration problem with context and actionable hint.
type ValidationError struct {
	Field   string
	Value   string
	Message string
	Hint    string
}

func (e ValidationError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("• [%s]: %s\n    Hint: %s", e.Field, e.Message, e.Hint)
	}
	return fmt.Sprintf("• [%s]: %s", e.Field, e.Message)
}

// ValidationErrors is a slice of ValidationError that implements error.
type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("configuration validation failed (%d error(s) found):\n", len(ve)))
	for _, err := range ve {
		sb.WriteString("  " + err.Error() + "\n")
	}
	return sb.String()
}

// Validate checks all fields in Config and returns a descriptive ValidationErrors if invalid.
func Validate(cfg *Config) error {
	if cfg == nil {
		return ValidationErrors{
			{
				Field:   "root",
				Message: "configuration object is nil",
				Hint:    "Ensure config.toml is non-empty and contains valid TOML syntax.",
			},
		}
	}

	var errs ValidationErrors

	// 1. Server validation
	if cfg.Server.Port != 0 && (cfg.Server.Port < 1 || cfg.Server.Port > 65535) {
		errs = append(errs, ValidationError{
			Field:   "server.port",
			Value:   fmt.Sprintf("%d", cfg.Server.Port),
			Message: fmt.Sprintf("port %d is out of valid range", cfg.Server.Port),
			Hint:    "TCP port must be between 1 and 65535 (default: 50051).",
		})
	}
	if cfg.Server.CacheSize < 0 {
		errs = append(errs, ValidationError{
			Field:   "server.cache_size",
			Value:   fmt.Sprintf("%d", cfg.Server.CacheSize),
			Message: "cache_size cannot be negative",
			Hint:    "Specify 0 to disable memory caching, or a positive integer (default: 10000).",
		})
	}

	// 2. Critique HTTP server validation
	if cfg.Critique.Port != 0 && (cfg.Critique.Port < 1 || cfg.Critique.Port > 65535) {
		errs = append(errs, ValidationError{
			Field:   "critique.port",
			Value:   fmt.Sprintf("%d", cfg.Critique.Port),
			Message: fmt.Sprintf("port %d is out of valid range", cfg.Critique.Port),
			Hint:    "HTTP port must be between 1 and 65535 (default: 8080).",
		})
	}
	if cfg.Critique.ProxyPort != 0 && (cfg.Critique.ProxyPort < 1 || cfg.Critique.ProxyPort > 65535) {
		errs = append(errs, ValidationError{
			Field:   "critique.proxy_port",
			Value:   fmt.Sprintf("%d", cfg.Critique.ProxyPort),
			Message: fmt.Sprintf("proxy_port %d is out of valid range", cfg.Critique.ProxyPort),
			Hint:    "Live reload proxy port must be between 1 and 65535 (default: 7331).",
		})
	}
	if cfg.Critique.Port != 0 && cfg.Critique.Port == cfg.Critique.ProxyPort {
		errs = append(errs, ValidationError{
			Field:   "critique.proxy_port",
			Value:   fmt.Sprintf("%d", cfg.Critique.ProxyPort),
			Message: "critique.proxy_port conflicts with critique.port",
			Hint:    "The live reload proxy and upstream HTTP server must listen on different ports (e.g. 7331 and 8080).",
		})
	}

	// 3. Critique Theme validation
	theme := cfg.Critique.Theme
	if theme.Preset != "" {
		normalizedPreset := strings.ToLower(strings.TrimSpace(theme.Preset))
		if !isPresetSupported(normalizedPreset) {
			closest := findClosestPreset(normalizedPreset)
			hint := fmt.Sprintf("Supported presets: %s.", strings.Join(SupportedPresets, ", "))
			if closest != "" {
				hint = fmt.Sprintf("Did you mean %q? %s", closest, hint)
			}
			errs = append(errs, ValidationError{
				Field:   "critique.theme.preset",
				Value:   theme.Preset,
				Message: fmt.Sprintf("unknown theme preset %q", theme.Preset),
				Hint:    hint,
			})
		}
	}

	// 4. Chroma Syntax Style validation
	if theme.SyntaxStyle != "" {
		styleName := strings.TrimSpace(theme.SyntaxStyle)
		if styles.Registry[styleName] == nil {
			errs = append(errs, ValidationError{
				Field:   "critique.theme.syntax_style",
				Value:   theme.SyntaxStyle,
				Message: fmt.Sprintf("unrecognized Chroma syntax style %q", theme.SyntaxStyle),
				Hint:    "Choose a valid Chroma style such as 'catppuccin-mocha', 'tokyonight-night', 'dracula', 'nord', or 'github-dark'.",
			})
		}
	}

	// 5. Custom Color Hex validation
	colorFields := map[string]string{
		"critique.theme.colors.bg_canvas":        theme.Colors.BGCanvas,
		"critique.theme.colors.bg_surface":       theme.Colors.BGSurface,
		"critique.theme.colors.bg_card":          theme.Colors.BGCard,
		"critique.theme.colors.border":           theme.Colors.Border,
		"critique.theme.colors.text_primary":     theme.Colors.TextPrimary,
		"critique.theme.colors.text_muted":       theme.Colors.TextMuted,
		"critique.theme.colors.accent":           theme.Colors.Accent,
		"critique.theme.colors.diff_add_bg":      theme.Colors.DiffAddBG,
		"critique.theme.colors.diff_add_border":  theme.Colors.DiffAddBorder,
		"critique.theme.colors.diff_del_bg":      theme.Colors.DiffDelBG,
		"critique.theme.colors.diff_del_border":  theme.Colors.DiffDelBorder,
		"critique.theme.colors.diff_mod_left_bg": theme.Colors.DiffModLeftBG,
		"critique.theme.colors.diff_mod_right_bg": theme.Colors.DiffModRightBG,
	}

	for field, val := range colorFields {
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		if !isValidColor(val) {
			errs = append(errs, ValidationError{
				Field:   field,
				Value:   val,
				Message: fmt.Sprintf("invalid color format %q", val),
				Hint:    "Must be a hex color code (e.g. '#1e1e2e') or CSS color string (e.g. 'rgba(166, 227, 161, 0.20)').",
			})
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func isPresetSupported(name string) bool {
	for _, p := range SupportedPresets {
		if p == name {
			return true
		}
	}
	return false
}

func isValidColor(val string) bool {
	return hexColorRegex.MatchString(val) || cssColorRegex.MatchString(val)
}

func findClosestPreset(input string) string {
	input = strings.ToLower(input)
	for _, p := range SupportedPresets {
		if strings.HasPrefix(p, input) || strings.Contains(p, input) {
			return p
		}
	}

	var bestMatch string
	minDist := 6
	for _, p := range SupportedPresets {
		d := levenshtein(input, p)
		if parts := strings.Split(p, "-"); len(parts) > 1 {
			if dPrefix := levenshtein(input, parts[0]); dPrefix < d {
				d = dPrefix
			}
		}
		if d < minDist {
			minDist = d
			bestMatch = p
		}
	}
	return bestMatch
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	d := make([][]int, la+1)
	for i := range d {
		d[i] = make([]int, lb+1)
		d[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		d[0][j] = j
	}

	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(
				d[i-1][j]+1,      // deletion
				d[i][j-1]+1,      // insertion
				d[i-1][j-1]+cost, // substitution
			)
		}
	}
	return d[la][lb]
}

func min(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
