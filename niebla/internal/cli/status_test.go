package cli

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"niebla/pkg/tree"
)

func TestScanScratchChanges(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "niebla-status-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create base tree with:
	// - "existing.txt"
	// - "to_delete.txt"
	// - "nested/orig.txt"
	baseExistingSHA := sha256.Sum256([]byte("existing original"))
	baseToDeleteSHA := sha256.Sum256([]byte("delete me"))
	baseNestedSHA := sha256.Sum256([]byte("nested orig"))

	nestedEntries := []tree.Entry{
		{
			Name:       "orig.txt",
			Mode:       0100644,
			Size:       uint64(len("nested orig")),
			BlobSHA256: baseNestedSHA[:],
		},
	}
	nestedTreeHash := tree.ComputeTreeHash(nestedEntries)

	baseEntries := []tree.Entry{
		{
			Name:       "existing.txt",
			Mode:       0100644,
			Size:       uint64(len("existing original")),
			BlobSHA256: baseExistingSHA[:],
		},
		{
			Name:        "nested",
			Mode:        0040755,
			SubTreeHash: nestedTreeHash,
		},
		{
			Name:       "to_delete.txt",
			Mode:       0100644,
			Size:       uint64(len("delete me")),
			BlobSHA256: baseToDeleteSHA[:],
		},
	}
	rootTreeHash := tree.ComputeTreeHash(baseEntries)

	treeMap := map[string][]tree.Entry{
		string(rootTreeHash):   baseEntries,
		string(nestedTreeHash): nestedEntries,
	}
	mockFetch := func(th []byte) ([]tree.Entry, error) {
		if e, ok := treeMap[string(th)]; ok {
			return e, nil
		}
		return []tree.Entry{}, nil
	}

	scratchDir := filepath.Join(tempDir, "scratch")
	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		t.Fatalf("failed to create scratch dir: %v", err)
	}

	// 1. Modified file: existing.txt
	if err := os.WriteFile(filepath.Join(scratchDir, "existing.txt"), []byte("modified content"), 0644); err != nil {
		t.Fatalf("failed to write existing.txt: %v", err)
	}

	// 2. Added file: added.txt
	if err := os.WriteFile(filepath.Join(scratchDir, "added.txt"), []byte("new file content"), 0644); err != nil {
		t.Fatalf("failed to write added.txt: %v", err)
	}

	// 3. Deleted file via prefix whiteout: .wh.to_delete.txt
	if err := os.WriteFile(filepath.Join(scratchDir, ".wh.to_delete.txt"), []byte(""), 0644); err != nil {
		t.Fatalf("failed to write whiteout: %v", err)
	}

	// 4. Added file in nested dir: nested/new_nested.txt
	if err := os.MkdirAll(filepath.Join(scratchDir, "nested"), 0755); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scratchDir, "nested", "new_nested.txt"), []byte("nested new"), 0644); err != nil {
		t.Fatalf("failed to write nested/new_nested.txt: %v", err)
	}

	// 5. Deleted file via .tombstone suffix: nested/orig.txt.tombstone
	if err := os.WriteFile(filepath.Join(scratchDir, "nested", "orig.txt.tombstone"), []byte(""), 0644); err != nil {
		t.Fatalf("failed to write tombstone: %v", err)
	}

	changes, err := ScanScratchChanges(scratchDir, rootTreeHash, mockFetch)
	if err != nil {
		t.Fatalf("ScanScratchChanges failed: %v", err)
	}

	changeTypes := make(map[string]ChangeType)
	for _, c := range changes {
		changeTypes[c.Path] = c.Type
	}

	if changeTypes["existing.txt"] != ChangeModified {
		t.Errorf("expected existing.txt to be Modified, got %v", changeTypes["existing.txt"])
	}
	if changeTypes["added.txt"] != ChangeAdded {
		t.Errorf("expected added.txt to be Added, got %v", changeTypes["added.txt"])
	}
	if changeTypes["to_delete.txt"] != ChangeDeleted {
		t.Errorf("expected to_delete.txt to be Deleted, got %v", changeTypes["to_delete.txt"])
	}
	if changeTypes["nested/new_nested.txt"] != ChangeAdded {
		t.Errorf("expected nested/new_nested.txt to be Added, got %v", changeTypes["nested/new_nested.txt"])
	}
	if changeTypes["nested/orig.txt"] != ChangeDeleted {
		t.Errorf("expected nested/orig.txt to be Deleted, got %v", changeTypes["nested/orig.txt"])
	}

	// Test renderStatus output
	var buf bytes.Buffer
	renderStatus(&buf, changes)
	output := buf.String()

	if !strings.Contains(output, "existing.txt") ||
		!strings.Contains(output, "added.txt") ||
		!strings.Contains(output, "to_delete.txt") {
		t.Errorf("renderStatus output missing expected entries: %s", output)
	}
}
