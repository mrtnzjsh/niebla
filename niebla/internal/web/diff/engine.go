package diff

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"html/template"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/sergi/go-diff/diffmatchpatch"
	"niebla/pkg/tree"
)

// LineType classifies a diff line in the side-by-side view.
type LineType string

const (
	LineEqual   LineType = "equal"
	LineAdded   LineType = "added"
	LineDeleted LineType = "deleted"
	LineEmpty   LineType = "empty"
)

// DiffLine represents a single line on one side (left or right) of the diff.
type DiffLine struct {
	Type            LineType
	LineNum         int
	Content         string
	HighlightedHTML template.HTML
}

// SideBySideRow represents a single aligned row comparing base and modified files.
type SideBySideRow struct {
	RowIndex int
	BaseLine DiffLine
	ModLine  DiffLine
	IsChange bool
	RowType  string // "equal", "modify", "add", "delete"
}

// FileDiffSummary summarizes changes to a single file between two Merkle trees.
type FileDiffSummary struct {
	Path               string
	Type               string // "M", "A", "D"
	Reviewed           bool   // Review status for active reviewer
	BaseBlobSHA        []byte
	ModifiedBlobSHA    []byte
	Additions          int
	Deletions          int
	UnresolvedComments int
}

// FileDiffDetail contains the full side-by-side diff rows and metadata for a file.
type FileDiffDetail struct {
	Path        string
	Type        string
	BaseBlobHex string
	ModBlobHex  string
	Rows        []SideBySideRow
	Additions   int
	Deletions   int
	IsBinary    bool
}

// DiffTrees performs recursive Merkle tree traversal between baseTreeHash and modTreeHash.
// It returns a slice of FileDiffSummary sorted alphabetically by path.
func DiffTrees(ctx context.Context, fetchTree tree.FetchTreeFunc, baseTreeHash, modTreeHash []byte) ([]FileDiffSummary, error) {
	var results []FileDiffSummary

	if bytes.Equal(baseTreeHash, modTreeHash) {
		return results, nil
	}

	err := traverseTrees(fetchTree, "", baseTreeHash, modTreeHash, &results)
	if err != nil {
		return nil, fmt.Errorf("failed to traverse Merkle trees: %w", err)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Path < results[j].Path
	})

	return results, nil
}

func traverseTrees(fetchTree tree.FetchTreeFunc, currentPath string, baseHash, modHash []byte, out *[]FileDiffSummary) error {
	var baseEntries []tree.Entry
	var modEntries []tree.Entry

	if len(baseHash) > 0 && !bytes.Equal(baseHash, tree.EmptyTreeHash) {
		entries, err := fetchTree(baseHash)
		if err != nil {
			return fmt.Errorf("failed to fetch base tree %s: %w", hex.EncodeToString(baseHash), err)
		}
		baseEntries = entries
	}

	if len(modHash) > 0 && !bytes.Equal(modHash, tree.EmptyTreeHash) {
		entries, err := fetchTree(modHash)
		if err != nil {
			return fmt.Errorf("failed to fetch mod tree %s: %w", hex.EncodeToString(modHash), err)
		}
		modEntries = entries
	}

	baseMap := make(map[string]tree.Entry, len(baseEntries))
	for _, e := range baseEntries {
		baseMap[e.Name] = e
	}

	modMap := make(map[string]tree.Entry, len(modEntries))
	for _, e := range modEntries {
		modMap[e.Name] = e
	}

	allNamesMap := make(map[string]struct{}, len(baseEntries)+len(modEntries))
	for _, e := range baseEntries {
		allNamesMap[e.Name] = struct{}{}
	}
	for _, e := range modEntries {
		allNamesMap[e.Name] = struct{}{}
	}

	names := make([]string, 0, len(allNamesMap))
	for name := range allNamesMap {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		filePath := path.Join(currentPath, name)
		baseEntry, inBase := baseMap[name]
		modEntry, inMod := modMap[name]

		if inBase && inMod {
			if baseEntry.IsDir() && modEntry.IsDir() {
				if !bytes.Equal(baseEntry.SubTreeHash, modEntry.SubTreeHash) {
					if err := traverseTrees(fetchTree, filePath, baseEntry.SubTreeHash, modEntry.SubTreeHash, out); err != nil {
						return err
					}
				}
			} else if !baseEntry.IsDir() && !modEntry.IsDir() {
				if !bytes.Equal(baseEntry.BlobSHA256, modEntry.BlobSHA256) {
					*out = append(*out, FileDiffSummary{
						Path:            filePath,
						Type:            "M",
						BaseBlobSHA:     baseEntry.BlobSHA256,
						ModifiedBlobSHA: modEntry.BlobSHA256,
					})
				}
			} else {
				// Replaced directory with file or vice-versa
				if err := collectSubtree(fetchTree, filePath, baseEntry, "D", out); err != nil {
					return err
				}
				if err := collectSubtree(fetchTree, filePath, modEntry, "A", out); err != nil {
					return err
				}
			}
		} else if inBase {
			if err := collectSubtree(fetchTree, filePath, baseEntry, "D", out); err != nil {
				return err
			}
		} else if inMod {
			if err := collectSubtree(fetchTree, filePath, modEntry, "A", out); err != nil {
				return err
			}
		}
	}

	return nil
}

func collectSubtree(fetchTree tree.FetchTreeFunc, currentPath string, entry tree.Entry, changeType string, out *[]FileDiffSummary) error {
	if !entry.IsDir() {
		summary := FileDiffSummary{
			Path: currentPath,
			Type: changeType,
		}
		if changeType == "D" {
			summary.BaseBlobSHA = entry.BlobSHA256
		} else {
			summary.ModifiedBlobSHA = entry.BlobSHA256
		}
		*out = append(*out, summary)
		return nil
	}

	entries, err := fetchTree(entry.SubTreeHash)
	if err != nil {
		return fmt.Errorf("failed to fetch subtree for %s: %w", currentPath, err)
	}

	for _, child := range entries {
		childPath := path.Join(currentPath, child.Name)
		if err := collectSubtree(fetchTree, childPath, child, changeType, out); err != nil {
			return err
		}
	}
	return nil
}

// ComputeSideBySideDiff runs Myers line diff via diffmatchpatch and formats syntax-highlighted side-by-side rows.
func ComputeSideBySideDiff(filePath string, baseContent, modContent []byte, changeType string) (*FileDiffDetail, error) {
	detail := &FileDiffDetail{
		Path: filePath,
		Type: changeType,
	}

	if isBinary(baseContent) || isBinary(modContent) {
		detail.IsBinary = true
		return detail, nil
	}

	baseStr := string(baseContent)
	modStr := string(modContent)

	baseHighlighted := HighlightLines(filePath, baseStr)
	modHighlighted := HighlightLines(filePath, modStr)

	dmp := diffmatchpatch.New()
	charsA, charsB, lineArray := dmp.DiffLinesToChars(baseStr, modStr)
	diffs := dmp.DiffMain(charsA, charsB, false)
	diffs = dmp.DiffCharsToLines(diffs, lineArray)

	var rows []SideBySideRow
	baseIdx := 0
	modIdx := 0
	rowIndex := 1
	additions := 0
	deletions := 0

	i := 0
	for i < len(diffs) {
		d := diffs[i]

		if d.Type == diffmatchpatch.DiffEqual {
			eqLines := splitLinesPreserving(d.Text)
			for _, lineContent := range eqLines {
				var baseHTML template.HTML
				if baseIdx < len(baseHighlighted) {
					baseHTML = baseHighlighted[baseIdx]
				} else {
					baseHTML = template.HTML(template.HTMLEscapeString(lineContent))
				}

				var modHTML template.HTML
				if modIdx < len(modHighlighted) {
					modHTML = modHighlighted[modIdx]
				} else {
					modHTML = template.HTML(template.HTMLEscapeString(lineContent))
				}

				rows = append(rows, SideBySideRow{
					RowIndex: rowIndex,
					BaseLine: DiffLine{
						Type:            LineEqual,
						LineNum:         baseIdx + 1,
						Content:         lineContent,
						HighlightedHTML: baseHTML,
					},
					ModLine: DiffLine{
						Type:            LineEqual,
						LineNum:         modIdx + 1,
						Content:         lineContent,
						HighlightedHTML: modHTML,
					},
					IsChange: false,
					RowType:  "equal",
				})
				rowIndex++
				baseIdx++
				modIdx++
			}
			i++
			continue
		}

		// Change block: collect consecutive deletes and inserts
		var delLines []string
		var insLines []string
		for i < len(diffs) && diffs[i].Type != diffmatchpatch.DiffEqual {
			if diffs[i].Type == diffmatchpatch.DiffDelete {
				delLines = append(delLines, splitLinesPreserving(diffs[i].Text)...)
			} else if diffs[i].Type == diffmatchpatch.DiffInsert {
				insLines = append(insLines, splitLinesPreserving(diffs[i].Text)...)
			}
			i++
		}

		deletions += len(delLines)
		additions += len(insLines)

		maxLen := len(delLines)
		if len(insLines) > maxLen {
			maxLen = len(insLines)
		}

		for k := 0; k < maxLen; k++ {
			var baseLine DiffLine
			var modLine DiffLine
			rowType := "equal"

			hasDel := k < len(delLines)
			hasIns := k < len(insLines)

			if hasDel && hasIns {
				rowType = "modify"
			} else if hasDel {
				rowType = "delete"
			} else {
				rowType = "add"
			}

			if hasDel {
				content := delLines[k]
				var hHTML template.HTML
				if baseIdx < len(baseHighlighted) {
					hHTML = baseHighlighted[baseIdx]
				} else {
					hHTML = template.HTML(template.HTMLEscapeString(content))
				}
				baseLine = DiffLine{
					Type:            LineDeleted,
					LineNum:         baseIdx + 1,
					Content:         content,
					HighlightedHTML: hHTML,
				}
				baseIdx++
			} else {
				baseLine = DiffLine{
					Type:    LineEmpty,
					LineNum: 0,
				}
			}

			if hasIns {
				content := insLines[k]
				var hHTML template.HTML
				if modIdx < len(modHighlighted) {
					hHTML = modHighlighted[modIdx]
				} else {
					hHTML = template.HTML(template.HTMLEscapeString(content))
				}
				modLine = DiffLine{
					Type:            LineAdded,
					LineNum:         modIdx + 1,
					Content:         content,
					HighlightedHTML: hHTML,
				}
				modIdx++
			} else {
				modLine = DiffLine{
					Type:    LineEmpty,
					LineNum: 0,
				}
			}

			rows = append(rows, SideBySideRow{
				RowIndex: rowIndex,
				BaseLine: baseLine,
				ModLine:  modLine,
				IsChange: true,
				RowType:  rowType,
			})
			rowIndex++
		}
	}

	detail.Rows = rows
	detail.Additions = additions
	detail.Deletions = deletions
	return detail, nil
}

var (
	syntaxStyleMu     sync.RWMutex
	activeSyntaxStyle = "dracula"
)

// SetSyntaxStyle configures the Chroma syntax highlighting theme (e.g. "catppuccin-mocha", "tokyonight-night").
func SetSyntaxStyle(styleName string) {
	syntaxStyleMu.Lock()
	defer syntaxStyleMu.Unlock()
	if strings.TrimSpace(styleName) != "" {
		activeSyntaxStyle = strings.TrimSpace(styleName)
	}
}

// GetSyntaxStyle returns the current Chroma syntax highlighting theme name.
func GetSyntaxStyle() string {
	syntaxStyleMu.RLock()
	defer syntaxStyleMu.RUnlock()
	return activeSyntaxStyle
}

// HighlightLines tokenizes and formats file content into HTML line slices using Chroma.
func HighlightLines(filename, content string) []template.HTML {
	if content == "" {
		return []template.HTML{}
	}

	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(content)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	syntaxStyleMu.RLock()
	currentStyle := activeSyntaxStyle
	syntaxStyleMu.RUnlock()

	style := styles.Get(currentStyle)
	if style == nil {
		style = styles.Fallback
	}

	formatter := html.New(
		html.Standalone(false),
		html.PreventSurroundingPre(true),
		html.WithClasses(false),
	)

	it, err := lexer.Tokenise(nil, content)
	if err != nil {
		rawLines := splitLinesPreserving(content)
		res := make([]template.HTML, len(rawLines))
		for idx, r := range rawLines {
			res[idx] = template.HTML(template.HTMLEscapeString(r))
		}
		return res
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, it); err != nil {
		rawLines := splitLinesPreserving(content)
		res := make([]template.HTML, len(rawLines))
		for idx, r := range rawLines {
			res[idx] = template.HTML(template.HTMLEscapeString(r))
		}
		return res
	}

	split := strings.Split(buf.String(), "\n")
	res := make([]template.HTML, len(split))
	for idx, s := range split {
		res[idx] = template.HTML(s)
	}
	return res
}

func splitLinesPreserving(s string) []string {
	if s == "" {
		return []string{}
	}
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	return strings.Split(s, "\n")
}

func isBinary(data []byte) bool {
	limit := 1024
	if len(data) < limit {
		limit = len(data)
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}
