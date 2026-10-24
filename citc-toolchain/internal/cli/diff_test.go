package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"citc-toolchain/pkg/tree"
)

func TestComputeFileDiff(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "citc-diff-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx := context.Background()

	// 1. Added file diff
	addedPath := filepath.Join(tempDir, "added.txt")
	if err := os.WriteFile(addedPath, []byte("line1\nline2\n"), 0644); err != nil {
		t.Fatalf("failed to write added file: %v", err)
	}

	addedChange := FileChange{
		Path:     "added.txt",
		Type:     ChangeAdded,
		DiskPath: addedPath,
	}

	diffAdded, err := computeFileDiff(ctx, nil, nil, addedChange)
	if err != nil {
		t.Fatalf("computeFileDiff for added failed: %v", err)
	}
	if !strings.Contains(diffAdded, "--- /dev/null") || !strings.Contains(diffAdded, "+++ b/added.txt") {
		t.Errorf("unexpected added diff header: %s", diffAdded)
	}
	if !strings.Contains(diffAdded, "+line1") || !strings.Contains(diffAdded, "+line2") {
		t.Errorf("added diff missing additions: %s", diffAdded)
	}

	// 2. Modified file diff with cache
	cacheDir := filepath.Join(tempDir, "cache")
	_ = os.MkdirAll(cacheDir, 0755)

	origContent := []byte("orig line 1\norig line 2\n")
	origSHA := computeSHA256(origContent)

	// Save original content in cacheDir (flat)
	if err := os.WriteFile(filepath.Join(cacheDir, strings.ToLower(hexEncode(origSHA))), origContent, 0644); err != nil {
		t.Fatalf("failed to write cached blob: %v", err)
	}

	modPath := filepath.Join(tempDir, "mod.txt")
	if err := os.WriteFile(modPath, []byte("orig line 1\nmodified line 2\nadded line 3\n"), 0644); err != nil {
		t.Fatalf("failed to write modified file: %v", err)
	}

	modChange := FileChange{
		Path:     "mod.txt",
		Type:     ChangeModified,
		DiskPath: modPath,
		BaseEntry: &tree.Entry{
			Name:       "mod.txt",
			BlobSHA256: origSHA,
		},
	}

	diffMod, err := computeFileDiff(ctx, nil, []string{cacheDir}, modChange)
	if err != nil {
		t.Fatalf("computeFileDiff for modified failed: %v", err)
	}
	if !strings.Contains(diffMod, "--- a/mod.txt") || !strings.Contains(diffMod, "+++ b/mod.txt") {
		t.Errorf("unexpected modified diff header: %s", diffMod)
	}
	if !strings.Contains(diffMod, "-orig line 2") || !strings.Contains(diffMod, "+modified line 2") {
		t.Errorf("modified diff missing expected lines: %s", diffMod)
	}

	// 3. Deleted file diff
	delChange := FileChange{
		Path: "mod.txt",
		Type: ChangeDeleted,
		BaseEntry: &tree.Entry{
			Name:       "mod.txt",
			BlobSHA256: origSHA,
		},
	}
	diffDel, err := computeFileDiff(ctx, nil, []string{cacheDir}, delChange)
	if err != nil {
		t.Fatalf("computeFileDiff for deleted failed: %v", err)
	}
	if !strings.Contains(diffDel, "--- a/mod.txt") || !strings.Contains(diffDel, "+++ /dev/null") {
		t.Errorf("unexpected deleted diff header: %s", diffDel)
	}

	// 4. Test renderColoredDiff output
	var buf bytes.Buffer
	renderColoredDiff(&buf, diffMod)
	if len(buf.String()) == 0 {
		t.Errorf("renderColoredDiff produced empty output")
	}
}

func hexEncode(b []byte) string {
	const hextable = "0123456789abcdef"
	dst := make([]byte, len(b)*2)
	for i, v := range b {
		dst[i*2] = hextable[v>>4]
		dst[i*2+1] = hextable[v&0x0f]
	}
	return string(dst)
}
