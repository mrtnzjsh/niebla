package ignore

import (
	"bufio"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Matcher evaluates whether a workspace-relative path should be ignored.
type Matcher interface {
	Match(relPath string, isDir bool) bool
}

// NoopMatcher never ignores anything except built-in .niebla metadata.
type NoopMatcher struct{}

// Match implements Matcher.
func (NoopMatcher) Match(relPath string, isDir bool) bool {
	return isBuiltinIgnored(normalizePath(relPath))
}

type rule struct {
	raw      string
	pattern  string
	negate   bool
	dirOnly  bool
	anchored bool
}

// PatternMatcher evaluates gitignore-style rules in declaration order.
// Later matching rules override earlier ones (supporting !negation).
type PatternMatcher struct {
	rules []rule
}

// NewMatcher constructs a PatternMatcher from pattern lines.
func NewMatcher(patterns ...string) *PatternMatcher {
	m := &PatternMatcher{}
	for _, line := range patterns {
		if r, ok := parseLine(line); ok {
			m.rules = append(m.rules, r)
		}
	}
	return m
}

// Parse constructs a PatternMatcher from an io.Reader containing .nieblaignore lines.
func Parse(r io.Reader) (*PatternMatcher, error) {
	scanner := bufio.NewScanner(r)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return NewMatcher(lines...), nil
}

// LoadFile loads a PatternMatcher from a .nieblaignore file on disk.
// If the file does not exist, a PatternMatcher with only built-in rules is returned without error.
func LoadFile(filePath string) (*PatternMatcher, error) {
	f, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewMatcher(), nil
		}
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

func parseLine(line string) (rule, bool) {
	line = strings.TrimRight(line, "\r\n")
	// Trim trailing unescaped spaces
	for strings.HasSuffix(line, " ") && !strings.HasSuffix(line, `\ `) {
		line = strings.TrimSuffix(line, " ")
	}
	line = strings.ReplaceAll(line, `\ `, " ")

	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return rule{}, false
	}

	var r rule
	r.raw = line
	p := line

	if strings.HasPrefix(p, `\!`) || strings.HasPrefix(p, `\#`) {
		p = p[1:]
	} else if strings.HasPrefix(p, "!") {
		r.negate = true
		p = p[1:]
	}

	if strings.HasSuffix(p, "/") {
		r.dirOnly = true
		p = strings.TrimSuffix(p, "/")
	}

	if strings.HasPrefix(p, "/") {
		r.anchored = true
		p = strings.TrimPrefix(p, "/")
	} else if strings.Contains(p, "/") {
		// Patterns containing a slash (other than trailing slash) are anchored to the root,
		// unless they start with "**/".
		if !strings.HasPrefix(p, "**/") {
			r.anchored = true
		}
	}

	p = strings.TrimPrefix(p, "./")
	if p == "" {
		return rule{}, false
	}

	r.pattern = p
	return r, true
}

func normalizePath(relPath string) string {
	norm := filepath.ToSlash(relPath)
	norm = strings.TrimPrefix(norm, "./")
	norm = strings.TrimPrefix(norm, "/")
	norm = strings.TrimSuffix(norm, "/")
	return norm
}

func isBuiltinIgnored(normPath string) bool {
	if normPath == "" || normPath == "." {
		return false
	}
	if normPath == ".niebla" || strings.HasPrefix(normPath, ".niebla/") {
		return true
	}
	parts := strings.Split(normPath, "/")
	for _, part := range parts {
		if part == ".niebla" {
			return true
		}
	}
	return false
}

// Match returns true if relPath should be ignored.
func (m *PatternMatcher) Match(relPath string, isDir bool) bool {
	norm := normalizePath(relPath)
	if norm == "" || norm == "." {
		return false
	}
	if isBuiltinIgnored(norm) {
		return true
	}
	if m == nil || len(m.rules) == 0 {
		return false
	}

	parts := strings.Split(norm, "/")
	ignored := false

	for _, r := range m.rules {
		if matchRule(r, norm, parts, isDir) {
			ignored = !r.negate
		}
	}

	return ignored
}

func matchRule(r rule, norm string, parts []string, isDir bool) bool {
	// Check if the path itself matches the rule
	if (!r.dirOnly || isDir) && matchPathPattern(r, norm, parts) {
		return true
	}

	// If a parent directory matches a non-negated directory rule (e.g., "build/" or "node_modules"),
	// any descendant path inside that directory also matches unless a later negation rule overrides it.
	for i := 1; i < len(parts); i++ {
		parentParts := parts[:i]
		parentPath := strings.Join(parentParts, "/")
		if matchPathPattern(r, parentPath, parentParts) {
			// If this is a negated rule (e.g. "!important.log"), matching a parent doesn't negate children
			// unless the rule specifically targets the directory or "**".
			if !r.negate || r.dirOnly {
				return true
			}
		}
	}

	return false
}

func matchPathPattern(r rule, candidate string, candidateParts []string) bool {
	pat := r.pattern

	if !r.anchored && !strings.Contains(pat, "/") {
		// Unanchored single-segment pattern (e.g. "*.o", "node_modules", "temp*"):
		// matches against the basename of candidate.
		base := candidateParts[len(candidateParts)-1]
		ok, _ := path.Match(pat, base)
		return ok
	}

	patParts := strings.Split(pat, "/")
	return matchSegments(patParts, candidateParts)
}

func matchSegments(patParts, pathParts []string) bool {
	if len(patParts) == 0 {
		return len(pathParts) == 0
	}

	if patParts[0] == "**" {
		// Collapse consecutive "**"
		for len(patParts) > 1 && patParts[1] == "**" {
			patParts = patParts[1:]
		}
		// "**" at end matches everything remaining
		if len(patParts) == 1 {
			return true
		}
		// Try matching the rest of patParts at every position in pathParts
		for i := 0; i <= len(pathParts); i++ {
			if matchSegments(patParts[1:], pathParts[i:]) {
				return true
			}
		}
		return false
	}

	if len(pathParts) == 0 {
		return false
	}

	ok, err := path.Match(patParts[0], pathParts[0])
	if err != nil || !ok {
		return false
	}

	return matchSegments(patParts[1:], pathParts[1:])
}
