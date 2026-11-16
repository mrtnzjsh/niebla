package ignore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatternMatcher_TableDriven(t *testing.T) {
	nieblaIgnoreContent := `
# Build outputs and dependencies
node_modules/
/dist
*.o
*.log

# Negate specific log files
!important.log
!logs/keep.log

# Double-star patterns
**/temp/**
docs/**/*.tmp
build/**/cache/

# Escaped characters and trailing spaces
\!literal_bang.txt
trailing_space\ 
`

	m, err := Parse(strings.NewReader(nieblaIgnoreContent))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	tests := []struct {
		name    string
		relPath string
		isDir   bool
		want    bool
	}{
		// Version-controlled .niebla directory rules (NOT ignored by default)
		{name: "root .niebla dir is tracked", relPath: ".niebla", isDir: true, want: false},
		{name: "file inside .niebla is tracked", relPath: ".niebla/OWNERS.toml", isDir: false, want: false},
		{name: "agents.toml inside .niebla is tracked", relPath: ".niebla/agents.toml", isDir: false, want: false},
		{name: "hooks.toml inside .niebla is tracked", relPath: ".niebla/hooks.toml", isDir: false, want: false},
		{name: "prompts inside .niebla is tracked", relPath: ".niebla/prompts/security.md", isDir: false, want: false},
		{name: ".nieblaignore itself is not ignored", relPath: ".nieblaignore", isDir: false, want: false},

		// Built-in daemon runtime socket rules
		{name: "nieblafs.sock is ignored", relPath: "nieblafs.sock", isDir: false, want: true},
		{name: "nested socket file is ignored", relPath: "sub/daemon.sock", isDir: false, want: true},

		// Directory-only rule: node_modules/
		{name: "root node_modules dir", relPath: "node_modules", isDir: true, want: true},
		{name: "nested node_modules dir", relPath: "web/node_modules", isDir: true, want: true},
		{name: "file inside node_modules", relPath: "node_modules/pkg/index.js", isDir: false, want: true},
		{name: "file named node_modules (not dir)", relPath: "node_modules", isDir: false, want: false},

		// Anchored root rule: /dist
		{name: "root dist dir", relPath: "dist", isDir: true, want: true},
		{name: "file inside root dist", relPath: "dist/bundle.js", isDir: false, want: true},
		{name: "nested dist dir is not matched by /dist", relPath: "pkg/dist", isDir: true, want: false},

		// Wildcard extension rule: *.o
		{name: "root object file", relPath: "main.o", isDir: false, want: true},
		{name: "nested object file", relPath: "src/core/engine.o", isDir: false, want: true},
		{name: "go source file", relPath: "src/core/engine.go", isDir: false, want: false},

		// Negation rules: *.log vs !important.log and !logs/keep.log
		{name: "generic log ignored", relPath: "debug.log", isDir: false, want: true},
		{name: "nested generic log ignored", relPath: "logs/app.log", isDir: false, want: true},
		{name: "negated root important.log", relPath: "important.log", isDir: false, want: false},
		{name: "negated nested important.log", relPath: "logs/important.log", isDir: false, want: false},
		{name: "negated anchored logs/keep.log", relPath: "logs/keep.log", isDir: false, want: false},
		{name: "non-matching path for anchored negation", relPath: "other/logs/keep.log", isDir: false, want: true},

		// Double-star rules: **/temp/**, docs/**/*.tmp, build/**/cache/
		{name: "root temp dir content", relPath: "temp/a.txt", isDir: false, want: true},
		{name: "nested temp dir content", relPath: "a/b/temp/c/d.txt", isDir: false, want: true},
		{name: "docs direct tmp file", relPath: "docs/draft.tmp", isDir: false, want: true},
		{name: "docs deeply nested tmp file", relPath: "docs/api/v2/spec.tmp", isDir: false, want: true},
		{name: "docs markdown file", relPath: "docs/api/v2/spec.md", isDir: false, want: false},
		{name: "build direct cache dir", relPath: "build/cache", isDir: true, want: true},
		{name: "build nested cache dir", relPath: "build/linux/amd64/cache", isDir: true, want: true},
		{name: "file inside build nested cache dir", relPath: "build/linux/amd64/cache/blob.bin", isDir: false, want: true},

		// Escaped bang and trailing space
		{name: "escaped leading bang", relPath: "!literal_bang.txt", isDir: false, want: true},
		{name: "escaped trailing space", relPath: "trailing_space ", isDir: false, want: true},
		{name: "unescaped without space not ignored", relPath: "trailing_space", isDir: false, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := m.Match(tc.relPath, tc.isDir)
			if got != tc.want {
				t.Errorf("Match(%q, isDir=%v) = %v, want %v", tc.relPath, tc.isDir, got, tc.want)
			}
		})
	}
}

func TestLoadFile_MissingAndPresent(t *testing.T) {
	tmpDir := t.TempDir()

	// Missing file returns default matcher containing DefaultPatterns without error
	m, err := LoadFile(filepath.Join(tmpDir, ".nieblaignore"))
	if err != nil {
		t.Fatalf("LoadFile on missing file returned error: %v", err)
	}
	if m.Match(".niebla/OWNERS.toml", false) {
		t.Errorf("expected .niebla/OWNERS.toml to NOT be ignored on missing file")
	}
	if !m.Match("nieblafs.sock", false) {
		t.Errorf("expected nieblafs.sock to be ignored as built-in runtime socket")
	}
	if !m.Match("node_modules/pkg/index.js", false) {
		t.Errorf("expected node_modules to be ignored by default")
	}
	if !m.Match(".direnv/flake-profile", false) {
		t.Errorf("expected .direnv to be ignored by default")
	}
	if !m.Match("bazel-bin/out", false) {
		t.Errorf("expected bazel-* to be ignored by default")
	}
	if !m.Match(".DS_Store", false) {
		t.Errorf("expected .DS_Store to be ignored by default")
	}
	if !m.Match(".env.local", false) {
		t.Errorf("expected .env.local to be ignored by default")
	}
	if m.Match("main.go", false) {
		t.Errorf("expected main.go not to be ignored")
	}

	// Present file: can add new rules and negate DefaultPatterns
	ignorePath := filepath.Join(tmpDir, ".nieblaignore")
	customContent := `
*.tmp
!keep.tmp
!node_modules/special
`
	if err := os.WriteFile(ignorePath, []byte(customContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m2, err := LoadFile(ignorePath)
	if err != nil {
		t.Fatalf("LoadFile on present file failed: %v", err)
	}
	if !m2.Match("foo.tmp", false) {
		t.Errorf("expected foo.tmp to be ignored")
	}
	if m2.Match("keep.tmp", false) {
		t.Errorf("expected keep.tmp to be negated (not ignored)")
	}
	if !m2.Match("node_modules/other/index.js", false) {
		t.Errorf("expected node_modules/other to be ignored by default")
	}
	if m2.Match("node_modules/special", false) {
		t.Errorf("expected negated node_modules/special not to be ignored")
	}
}

func TestPatternMatcher_DotNieblaGovernance(t *testing.T) {
	m := NewDefaultMatcher()

	// Assert required Issue #4 governance paths are not ignored
	if m.Match(".niebla", true) {
		t.Errorf("expected .niebla directory not to be ignored")
	}
	if m.Match(".niebla/OWNERS.toml", false) {
		t.Errorf("expected .niebla/OWNERS.toml not to be ignored")
	}
	if m.Match(".niebla/agents.toml", false) {
		t.Errorf("expected .niebla/agents.toml not to be ignored")
	}
	if m.Match(".niebla/hooks.toml", false) {
		t.Errorf("expected .niebla/hooks.toml not to be ignored")
	}
	if m.Match(".niebla/prompts/security.md", false) {
		t.Errorf("expected .niebla/prompts/security.md not to be ignored")
	}

	// Sockets should be ignored
	if !m.Match("nieblafs.sock", false) {
		t.Errorf("expected nieblafs.sock to be ignored")
	}
	if !m.Match("nested/service.sock", false) {
		t.Errorf("expected nested/service.sock to be ignored")
	}

	// Verify selective ignore inside .niebla works via custom rules
	customMatcher := NewDefaultMatcher(".niebla/local-cache/")
	if !customMatcher.Match(".niebla/local-cache/temp.json", false) {
		t.Errorf("expected .niebla/local-cache/temp.json to be ignored when explicitly in rules")
	}
	if customMatcher.Match(".niebla/OWNERS.toml", false) {
		t.Errorf("expected .niebla/OWNERS.toml to remain tracked")
	}
}
