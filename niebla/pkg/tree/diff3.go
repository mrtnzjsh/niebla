package tree

import (
	"bytes"
	"fmt"

	"github.com/pmezard/go-difflib/difflib"
)

// editChunk represents a change chunk in original lines [oStart, oEnd) replaced with newLines.
type editChunk struct {
	oStart   int
	oEnd     int
	newLines []string
}

func handleOverlappingDiffChunks(
	origLines, theirsLines, mineLines []string,
	theirsChunks, mineChunks []editChunk,
	tIdx, mIdx *int,
	theirsLabel, mineLabel string,
	hasConflict *bool,
	conflictHunks *int,
) ([]string, int) {
	tc := theirsChunks[*tIdx]
	mc := mineChunks[*mIdx]

	unionStart := min(tc.oStart, mc.oStart)
	unionEnd := max(tc.oEnd, mc.oEnd)

	*tIdx++
	*mIdx++
	for (*tIdx < len(theirsChunks) && theirsChunks[*tIdx].oStart < unionEnd) ||
		(*mIdx < len(mineChunks) && mineChunks[*mIdx].oStart < unionEnd) {
		if *tIdx < len(theirsChunks) && theirsChunks[*tIdx].oStart < unionEnd {
			if theirsChunks[*tIdx].oEnd > unionEnd {
				unionEnd = theirsChunks[*tIdx].oEnd
			}
			*tIdx++
		}
		if *mIdx < len(mineChunks) && mineChunks[*mIdx].oStart < unionEnd {
			if mineChunks[*mIdx].oEnd > unionEnd {
				unionEnd = mineChunks[*mIdx].oEnd
			}
			*mIdx++
		}
	}

	tRepl := reconstructRegion(origLines, theirsChunks, unionStart, unionEnd, theirsLines)
	mRepl := reconstructRegion(origLines, mineChunks, unionStart, unionEnd, mineLines)

	var res []string
	if slicesEqual(tRepl, mRepl) {
		res = append(res, tRepl...)
	} else {
		*hasConflict = true
		*conflictHunks++
		res = append(res, fmt.Sprintf("<<<<<<< %s\n", theirsLabel))
		res = append(res, tRepl...)
		res = append(res, "=======\n")
		res = append(res, mRepl...)
		res = append(res, fmt.Sprintf(">>>>>>> %s\n", mineLabel))
	}
	return res, unionEnd
}

func copyUnchangedLines(origLines []string, theirsChunks, mineChunks []editChunk, tIdx, mIdx int, origIdx *int) []string {
	nextOrig := len(origLines)
	if tIdx < len(theirsChunks) && theirsChunks[tIdx].oStart < nextOrig {
		nextOrig = theirsChunks[tIdx].oStart
	}
	if mIdx < len(mineChunks) && mineChunks[mIdx].oStart < nextOrig {
		nextOrig = mineChunks[mIdx].oStart
	}

	var res []string
	for *origIdx < nextOrig && *origIdx < len(origLines) {
		res = append(res, origLines[*origIdx])
		*origIdx++
	}
	return res
}

// Merge3Way performs a 3-way line-based merge between ancestor, theirs, and mine.
// If overlapping changes cannot be resolved, standard conflict markers (<<<<<<<, =======, >>>>>>>)
// are injected with the provided labels.
func Merge3Way(ancestor, theirs, mine []byte, theirsLabel, mineLabel string) (merged []byte, hasConflict bool, conflictHunks int) {
	if bytes.Equal(mine, ancestor) {
		return append([]byte(nil), theirs...), false, 0
	}
	if bytes.Equal(theirs, ancestor) {
		return append([]byte(nil), mine...), false, 0
	}
	if bytes.Equal(mine, theirs) {
		return append([]byte(nil), mine...), false, 0
	}

	if theirsLabel == "" {
		theirsLabel = "THEIRS"
	}
	if mineLabel == "" {
		mineLabel = "MINE"
	}

	origLines := splitLines(string(ancestor))
	theirsLines := splitLines(string(theirs))
	mineLines := splitLines(string(mine))

	theirsChunks := getEditChunks(origLines, theirsLines)
	mineChunks := getEditChunks(origLines, mineLines)

	var result []string
	origIdx := 0
	tIdx := 0
	mIdx := 0

	for origIdx < len(origLines) || tIdx < len(theirsChunks) || mIdx < len(mineChunks) {
		tHas := tIdx < len(theirsChunks) && theirsChunks[tIdx].oStart <= origIdx
		mHas := mIdx < len(mineChunks) && mineChunks[mIdx].oStart <= origIdx

		if !tHas && !mHas {
			unchanged := copyUnchangedLines(origLines, theirsChunks, mineChunks, tIdx, mIdx, &origIdx)
			result = append(result, unchanged...)
			continue
		}

		if tHas && mHas {
			chunkLines, nextOrig := handleOverlappingDiffChunks(
				origLines, theirsLines, mineLines,
				theirsChunks, mineChunks,
				&tIdx, &mIdx,
				theirsLabel, mineLabel,
				&hasConflict, &conflictHunks,
			)
			result = append(result, chunkLines...)
			origIdx = nextOrig
			continue
		}

		if tHas {
			tc := theirsChunks[tIdx]
			tIdx++
			result = append(result, tc.newLines...)
			origIdx = tc.oEnd
			continue
		}

		if mHas {
			mc := mineChunks[mIdx]
			mIdx++
			result = append(result, mc.newLines...)
			origIdx = mc.oEnd
			continue
		}
	}

	var buf bytes.Buffer
	for _, l := range result {
		buf.WriteString(l)
	}
	return buf.Bytes(), hasConflict, conflictHunks
}

func getEditChunks(orig, modified []string) []editChunk {
	m := difflib.NewMatcher(orig, modified)
	opcodes := m.GetOpCodes()

	var chunks []editChunk
	for _, op := range opcodes {
		if op.Tag == 'e' {
			continue
		}
		var newLines []string
		if op.J2 > op.J1 {
			newLines = append(newLines, modified[op.J1:op.J2]...)
		}
		chunks = append(chunks, editChunk{
			oStart:   op.I1,
			oEnd:     op.I2,
			newLines: newLines,
		})
	}
	return chunks
}

func reconstructRegion(orig []string, chunks []editChunk, start, end int, fullMod []string) []string {
	var res []string
	curr := start

	for _, c := range chunks {
		// Include zero-width insertion chunks (c.oStart == start && c.oEnd == end)
		if !(c.oStart == start && c.oEnd == end) && (c.oEnd <= start || c.oStart >= end) {
			continue
		}
		// Copy unchanged prefix inside the region
		for curr < c.oStart && curr < len(orig) {
			res = append(res, orig[curr])
			curr++
		}
		res = append(res, c.newLines...)
		curr = c.oEnd
	}

	// Copy remaining unchanged suffix inside region
	for curr < end && curr < len(orig) {
		res = append(res, orig[curr])
		curr++
	}
	return res
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func splitLines(text string) []string {
	if len(text) == 0 {
		return nil
	}
	var lines []string
	start := 0
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			lines = append(lines, text[start:i+1])
			start = i + 1
		}
	}
	if start < len(text) {
		lines = append(lines, text[start:])
	}
	return lines
}
