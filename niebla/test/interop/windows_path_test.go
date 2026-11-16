package interop_test

import (
	"strings"
	"testing"

	"niebla/pkg/ignore"
)

func TestWindowsBackslashNormalization_InIgnoreMatcher(t *testing.T) {
	matcher := ignore.NewMatcher(
		"node_modules/",
		"build/",
		"*.tmp",
		"docs/specs/*.pdf",
	)

	testCases := []struct {
		inputPath string
		isDir     bool
		expected  bool
	}{
		// Standard POSIX paths
		{"node_modules/express/index.js", false, true},
		{"build/output.bin", false, true},
		{"src/temp.tmp", false, true},
		{"docs/specs/v1.pdf", false, true},
		{"src/main.go", false, false},

		// Windows backslash paths
		{`node_modules\express\index.js`, false, true},
		{`build\output.bin`, false, true},
		{`src\temp.tmp`, false, true},
		{`docs\specs\v1.pdf`, false, true},
		{`src\main.go`, false, false},

		// Mixed separators
		{`node_modules/express\lib/index.js`, false, true},
		{`docs\specs/v1.pdf`, false, true},
	}

	for _, tc := range testCases {
		matched := matcher.Match(tc.inputPath, tc.isDir)
		if matched != tc.expected {
			t.Errorf("matcher.Match(%q, %v) = %v; expected %v", tc.inputPath, tc.isDir, matched, tc.expected)
		}
	}
}

func TestWindowsVolumePathHandling(t *testing.T) {
	// Verify that Windows paths with volume labels or relative prefix are stripped cleanly
	stripVolumeOrPrefix := func(p string) string {
		p = strings.ReplaceAll(p, "\\", "/")
		if len(p) >= 2 && p[1] == ':' {
			p = p[2:]
		}
		p = strings.TrimPrefix(p, "/")
		return p
	}

	testCases := []struct {
		windowsPath string
		expected    string
	}{
		{`C:\Users\matatan\workspace\src\main.go`, `Users/matatan/workspace/src/main.go`},
		{`D:\niebla\workspaces\test\pkg\auth.go`, `niebla/workspaces/test/pkg/auth.go`},
		{`src\nested\service.go`, `src/nested/service.go`},
	}

	for _, tc := range testCases {
		normalized := stripVolumeOrPrefix(tc.windowsPath)
		if normalized != tc.expected {
			t.Errorf("stripVolumeOrPrefix(%q) = %q; expected %q", tc.windowsPath, normalized, tc.expected)
		}
	}
}
