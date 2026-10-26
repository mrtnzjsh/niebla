package diff

import (
	"context"
	"testing"

	"niebla/pkg/tree"
)

func TestDiffTrees(t *testing.T) {
	ctx := context.Background()

	// Setup fake trees
	file1Blob := []byte{1, 2, 3}
	file2Blob := []byte{4, 5, 6}
	file2Modified := []byte{7, 8, 9}

	baseEntries := []tree.Entry{
		{Name: "common.go", Mode: 0100644, Size: 10, BlobSHA256: file1Blob},
		{Name: "modified.go", Mode: 0100644, Size: 20, BlobSHA256: file2Blob},
		{Name: "deleted.go", Mode: 0100644, Size: 15, BlobSHA256: file1Blob},
	}
	baseHash := tree.ComputeTreeHash(baseEntries)

	modEntries := []tree.Entry{
		{Name: "added.go", Mode: 0100644, Size: 30, BlobSHA256: file1Blob},
		{Name: "common.go", Mode: 0100644, Size: 10, BlobSHA256: file1Blob},
		{Name: "modified.go", Mode: 0100644, Size: 25, BlobSHA256: file2Modified},
	}
	modHash := tree.ComputeTreeHash(modEntries)

	treeStore := map[string][]tree.Entry{
		string(baseHash): baseEntries,
		string(modHash):  modEntries,
	}

	fetchTree := func(hash []byte) ([]tree.Entry, error) {
		return treeStore[string(hash)], nil
	}

	diffs, err := DiffTrees(ctx, fetchTree, baseHash, modHash)
	if err != nil {
		t.Fatalf("DiffTrees failed: %v", err)
	}

	if len(diffs) != 3 {
		t.Fatalf("expected 3 diffs, got %d", len(diffs))
	}

	// Alphabetical order: added.go, deleted.go, modified.go
	if diffs[0].Path != "added.go" || diffs[0].Type != "A" {
		t.Errorf("expected added.go [A], got %+v", diffs[0])
	}
	if diffs[1].Path != "deleted.go" || diffs[1].Type != "D" {
		t.Errorf("expected deleted.go [D], got %+v", diffs[1])
	}
	if diffs[2].Path != "modified.go" || diffs[2].Type != "M" {
		t.Errorf("expected modified.go [M], got %+v", diffs[2])
	}
}

func TestComputeSideBySideDiff(t *testing.T) {
	base := []byte("line 1\nline 2\nline 3\n")
	mod := []byte("line 1\nline 2 modified\nline 3\nline 4 added\n")

	detail, err := ComputeSideBySideDiff("test.txt", base, mod, "M")
	if err != nil {
		t.Fatalf("ComputeSideBySideDiff failed: %v", err)
	}

	if detail.Additions != 2 {
		t.Errorf("expected 2 additions, got %d", detail.Additions)
	}
	if detail.Deletions != 1 {
		t.Errorf("expected 1 deletion, got %d", detail.Deletions)
	}

	if len(detail.Rows) == 0 {
		t.Fatalf("expected rows, got 0")
	}

	// Verify rows structure
	foundModify := false
	foundAdd := false
	for _, r := range detail.Rows {
		if r.RowType == "modify" {
			foundModify = true
		}
		if r.RowType == "add" {
			foundAdd = true
		}
	}

	if !foundModify {
		t.Errorf("expected a modify row for line 2")
	}
	if !foundAdd {
		t.Errorf("expected an add row for line 4")
	}
}
