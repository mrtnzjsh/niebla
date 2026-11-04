package config

import (
	"fmt"
	"regexp"
	"strings"
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

	validators := []func(*Config, *ValidationErrors) error{
		validateVersion,
		validateCoreSettings,
		validateServerDaemon,
		validateServerTLS,
		validateServerAuthTokens,
		validateFSConfig,
		validateCritiqueHTTPServer,
		validateInterServicePortCollisions,
		validateCritiqueDefaultReviewer,
		validateCritiqueAuthMode,
		validateRequiredApprovals,
		validateGroupsMode,
		validateCritiqueTheme,
		validateCritiqueAI,
		validateEvents,
		validateResilience,
		validateCI,
	}

	for _, validator := range validators {
		_ = validator(cfg, &errs)
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func isPresetSupported(name string) bool {
	for _, preset := range SupportedPresets {
		if preset == name {
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
	for _, preset := range SupportedPresets {
		if strings.HasPrefix(preset, input) || strings.Contains(preset, input) {
			return preset
		}
	}

	var bestMatch string
	minDist := 6
	for _, preset := range SupportedPresets {
		dist := levenshtein(input, preset)
		if parts := strings.Split(preset, "-"); len(parts) > 1 {
			if dPrefix := levenshtein(input, parts[0]); dPrefix < dist {
				dist = dPrefix
			}
		}
		if dist < minDist {
			minDist = dist
			bestMatch = preset
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
	minVal := vals[0]
	for _, val := range vals[1:] {
		if val < minVal {
			minVal = val
		}
	}
	return minVal
}
