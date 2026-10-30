package ai

import (
	"context"
	"fmt"
	"strings"
)

const (
	ApertureLines = "lines"
	ApertureBlock = "block"
	ApertureFile  = "file"
)

// ExtractContext slices the file content into the exact target lines [lineStart, lineEnd] (1-indexed)
// and the surrounding context corresponding to the requested aperture tier ("lines", "block", or "file").
func ExtractContext(fileContent string, lineStart, lineEnd int, tier string, maxContextLines int) (targetCode string, surrounding string) {
	lines := strings.Split(strings.ReplaceAll(fileContent, "\r\n", "\n"), "\n")
	total := len(lines)
	if total == 0 {
		return "", ""
	}

	if lineStart < 1 {
		lineStart = 1
	}
	if lineEnd < lineStart {
		lineEnd = lineStart
	}
	if lineStart > total {
		lineStart = total
	}
	if lineEnd > total {
		lineEnd = total
	}

	targetSlice := lines[lineStart-1 : lineEnd]
	targetCode = strings.Join(targetSlice, "\n")

	if maxContextLines <= 0 {
		maxContextLines = 1200
	}

	switch tier {
	case ApertureLines, "":
		return targetCode, ""

	case ApertureBlock:
		startIdx, endIdx := findEnclosingBlock(lines, lineStart-1, lineEnd-1, 30)
		return targetCode, formatNumberedLines(lines, startIdx, endIdx)

	case ApertureFile:
		startIdx := 0
		endIdx := total - 1
		if total > maxContextLines {
			mid := (lineStart + lineEnd) / 2
			half := maxContextLines / 2
			startIdx = mid - half
			if startIdx < 0 {
				startIdx = 0
			}
			endIdx = startIdx + maxContextLines - 1
			if endIdx >= total {
				endIdx = total - 1
				startIdx = endIdx - maxContextLines + 1
				if startIdx < 0 {
					startIdx = 0
				}
			}
		}
		return targetCode, formatNumberedLines(lines, startIdx, endIdx)

	default:
		return targetCode, ""
	}
}

// findEnclosingBlock locates the enclosing function/type block around [startIdx, endIdx] (0-indexed),
// expanding at least radius lines or up to top-level declaration boundaries.
func findEnclosingBlock(lines []string, startIdx, endIdx, radius int) (int, int) {
	n := len(lines)
	blockStart := startIdx
	for i := startIdx; i >= 0 && i >= startIdx-radius*2; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if isBlockBoundaryStart(lines[i], trimmed) {
			blockStart = i
			break
		}
	}
	if startIdx-blockStart < 10 {
		blockStart = startIdx - radius
		if blockStart < 0 {
			blockStart = 0
		}
	}

	blockEnd := endIdx
	for i := endIdx; i < n && i <= endIdx+radius*2; i++ {
		if lines[i] == "}" || strings.HasPrefix(lines[i], "func ") || strings.HasPrefix(lines[i], "def ") {
			blockEnd = i
			if lines[i] == "}" {
				break
			}
		}
	}
	if blockEnd-endIdx < 10 {
		blockEnd = endIdx + radius
		if blockEnd >= n {
			blockEnd = n - 1
		}
	}

	return blockStart, blockEnd
}

func isBlockBoundaryStart(rawLine, trimmed string) bool {
	if len(rawLine) > 0 && rawLine[0] != ' ' && rawLine[0] != '\t' {
		for _, prefix := range []string{"func ", "type ", "def ", "class ", "fn ", "pub fn ", "export ", "function "} {
			if strings.HasPrefix(trimmed, prefix) {
				return true
			}
		}
	}
	return false
}

func formatNumberedLines(lines []string, startIdx, endIdx int) string {
	var b strings.Builder
	for i := startIdx; i <= endIdx && i < len(lines); i++ {
		fmt.Fprintf(&b, "%4d | %s\n", i+1, lines[i])
	}
	return strings.TrimRight(b.String(), "\n")
}

// NeedsInitialBlockAperture heuristically checks if the target lines or comment immediately
// require block-level context (e.g., single closing brace, empty target, or comment referencing signature/imports).
func NeedsInitialBlockAperture(targetCode, comment string) bool {
	trimmedCode := strings.TrimSpace(targetCode)
	if len(trimmedCode) <= 3 {
		return true
	}
	lowerComment := strings.ToLower(comment)
	for _, kw := range []string{"signature", "return type", "import", "struct", "interface", "caller", "whole function", "enclosing"} {
		if strings.Contains(lowerComment, kw) {
			return true
		}
	}
	return false
}

// GenerateWithAperture executes adaptive context escalation:
// 1. Starts with ApertureLines ("lines") — or ApertureBlock if NeedsInitialBlockAperture is true.
// 2. If the model reports SufficientContext == false, widens to ApertureBlock ("block").
// 3. If still insufficient, widens to ApertureFile ("file", up to maxContextLines).
func GenerateWithAperture(ctx context.Context, client *Client, fileContent string, baseReq HintRequest, maxContextLines int) (*HintResponse, error) {
	tiers := []string{ApertureLines, ApertureBlock, ApertureFile}
	if NeedsInitialBlockAperture(ExtractTargetOnly(fileContent, baseReq.LineStart, baseReq.LineEnd), baseReq.ReviewerComment) {
		tiers = []string{ApertureBlock, ApertureFile}
	}

	var lastResp *HintResponse
	var lastErr error

	for _, tier := range tiers {
		targetCode, surrounding := ExtractContext(fileContent, baseReq.LineStart, baseReq.LineEnd, tier, maxContextLines)
		req := baseReq
		req.TargetCode = targetCode
		req.SurroundingContext = surrounding
		req.ApertureLevel = tier

		resp, err := client.GenerateHint(ctx, req)
		if err != nil {
			lastErr = err
			break
		}
		lastResp = resp
		if resp.SufficientContext {
			return resp, nil
		}
	}

	if lastResp != nil {
		return lastResp, nil
	}
	return nil, lastErr
}

// ExtractTargetOnly returns only the 1-indexed lines [lineStart, lineEnd] from fileContent.
func ExtractTargetOnly(fileContent string, lineStart, lineEnd int) string {
	target, _ := ExtractContext(fileContent, lineStart, lineEnd, ApertureLines, 0)
	return target
}

