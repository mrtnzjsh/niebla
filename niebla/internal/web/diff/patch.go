package diff

import (
	"fmt"
	"strings"
)

// ApplyLineRangeReplacement replaces the 1-indexed inclusive line range [startLine, endLine]
// in originalContent with replacementCode, preserving trailing newline conventions.
func ApplyLineRangeReplacement(originalContent string, startLine, endLine int, replacementCode string) (string, error) {
	normalized := strings.ReplaceAll(originalContent, "\r\n", "\n")
	hasTrailingNewline := strings.HasSuffix(normalized, "\n")
	trimmed := strings.TrimSuffix(normalized, "\n")

	var lines []string
	if trimmed != "" {
		lines = strings.Split(trimmed, "\n")
	}

	if startLine < 1 {
		startLine = 1
	}
	if endLine < startLine {
		endLine = startLine
	}
	if len(lines) == 0 {
		if hasTrailingNewline && !strings.HasSuffix(replacementCode, "\n") {
			return replacementCode + "\n", nil
		}
		return replacementCode, nil
	}
	if startLine > len(lines) {
		return "", fmt.Errorf("startLine %d exceeds file line count %d", startLine, len(lines))
	}
	if endLine > len(lines) {
		endLine = len(lines)
	}

	before := lines[:startLine-1]
	after := lines[endLine:]

	cleanReplacement := strings.TrimSuffix(strings.ReplaceAll(replacementCode, "\r\n", "\n"), "\n")
	var replacementLines []string
	if cleanReplacement != "" || replacementCode == "" {
		if replacementCode != "" {
			replacementLines = strings.Split(cleanReplacement, "\n")
		}
	}

	resultLines := make([]string, 0, len(before)+len(replacementLines)+len(after))
	resultLines = append(resultLines, before...)
	resultLines = append(resultLines, replacementLines...)
	resultLines = append(resultLines, after...)

	out := strings.Join(resultLines, "\n")
	if hasTrailingNewline && out != "" {
		out += "\n"
	}
	return out, nil
}

