package tree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"strings"
	"testing"
)

type memoryStorage struct {
	blobs map[string][]byte
	trees map[string][]Entry
}

func newMemoryStorage() *memoryStorage {
	return &memoryStorage{
		blobs: make(map[string][]byte),
		trees: make(map[string][]Entry),
	}
}

func (m *memoryStorage) writeBlob(data []byte) ([]byte, error) {
	h := sha256.Sum256(data)
	m.blobs[string(h[:])] = append([]byte(nil), data...)
	return h[:], nil
}

func (m *memoryStorage) readBlob(sha []byte) ([]byte, error) {
	data, ok := m.blobs[string(sha)]
	if !ok {
		return nil, nil
	}
	return data, nil
}

func (m *memoryStorage) writeTree(entries []Entry) ([]byte, error) {
	h := ComputeTreeHash(entries)
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	SortEntries(sorted)
	m.trees[string(h)] = sorted
	return h, nil
}

func (m *memoryStorage) fetchTree(treeHash []byte) ([]Entry, error) {
	entries, ok := m.trees[string(treeHash)]
	if !ok {
		return nil, nil
	}
	return entries, nil
}

func TestMerge3Way_LineLevel(t *testing.T) {
	t.Run("Clean non-overlapping changes", func(t *testing.T) {
		ancestor := []byte("line1\nline2\nline3\nline4\nline5\n")
		theirs := []byte("HEADER\nline1\nline2\nline3\nline4\nline5\n")
		mine := []byte("line1\nline2\nline3\nline4\nline5\nFOOTER\n")

		merged, hasConflict, hunks := Merge3Way(ancestor, theirs, mine, "PARENT", "CHILD")
		if hasConflict {
			t.Fatalf("expected clean merge, got conflict with %d hunks:\n%s", hunks, string(merged))
		}
		expected := "HEADER\nline1\nline2\nline3\nline4\nline5\nFOOTER\n"
		if string(merged) != expected {
			t.Errorf("expected:\n%q\ngot:\n%q", expected, string(merged))
		}
	})

	t.Run("Conflicting overlapping changes", func(t *testing.T) {
		ancestor := []byte("line1\nline2\nline3\n")
		theirs := []byte("line1\nPARENT EDIT\nline3\n")
		mine := []byte("line1\nCHILD EDIT\nline3\n")

		merged, hasConflict, hunks := Merge3Way(ancestor, theirs, mine, "PARENT", "CHILD")
		if !hasConflict {
			t.Fatalf("expected conflict, got clean merge:\n%s", string(merged))
		}
		if hunks != 1 {
			t.Errorf("expected 1 conflict hunk, got %d", hunks)
		}
		if !strings.Contains(string(merged), "<<<<<<< PARENT") ||
			!strings.Contains(string(merged), "PARENT EDIT") ||
			!strings.Contains(string(merged), "=======") ||
			!strings.Contains(string(merged), "CHILD EDIT") ||
			!strings.Contains(string(merged), ">>>>>>> CHILD") {
			t.Errorf("conflict markers not properly formed:\n%s", string(merged))
		}
	})

	t.Run("Identical edits on both sides", func(t *testing.T) {
		ancestor := []byte("line1\nline2\nline3\n")
		theirs := []byte("line1\nSHARED EDIT\nline3\n")
		mine := []byte("line1\nSHARED EDIT\nline3\n")

		merged, hasConflict, _ := Merge3Way(ancestor, theirs, mine, "PARENT", "CHILD")
		if hasConflict {
			t.Fatalf("expected clean merge for identical edits, got conflict")
		}
		expected := "line1\nSHARED EDIT\nline3\n"
		if string(merged) != expected {
			t.Errorf("expected:\n%s\ngot:\n%s", expected, string(merged))
		}
	})
}

func TestThreeWayTreeMerge(t *testing.T) {
	ctx := context.Background()

	t.Run("Clean Fast-Path Identical", func(t *testing.T) {
		storage := newMemoryStorage()
		treeHash := ComputeTreeHash(nil)
		merged, conflicts, err := ThreeWayTreeMerge(
			ctx, treeHash, treeHash, treeHash,
			storage.fetchTree, storage.readBlob, storage.writeBlob, storage.writeTree,
			"THEIRS", "MINE",
		)
		if err != nil || len(conflicts) > 0 {
			t.Fatalf("unexpected error: %v, conflicts: %v", err, conflicts)
		}
		if !bytes.Equal(merged, treeHash) {
			t.Errorf("expected merged to equal input tree hash")
		}
	})

	t.Run("Independent File Modifications", func(t *testing.T) {
		storage := newMemoryStorage()

		// Base tree: fileA, fileB
		bA, _ := storage.writeBlob([]byte("fileA original\n"))
		bB, _ := storage.writeBlob([]byte("fileB original\n"))
		ancHash, _ := storage.writeTree([]Entry{
			{Name: "fileA.txt", Mode: 0100644, Size: 15, BlobSHA256: bA},
			{Name: "fileB.txt", Mode: 0100644, Size: 15, BlobSHA256: bB},
		})

		// Theirs modifies fileA
		bA_mod, _ := storage.writeBlob([]byte("fileA modified by parent\n"))
		theirsHash, _ := storage.writeTree([]Entry{
			{Name: "fileA.txt", Mode: 0100644, Size: 25, BlobSHA256: bA_mod},
			{Name: "fileB.txt", Mode: 0100644, Size: 15, BlobSHA256: bB},
		})

		// Mine modifies fileB and adds fileC
		bB_mod, _ := storage.writeBlob([]byte("fileB modified by child\n"))
		bC, _ := storage.writeBlob([]byte("fileC added by child\n"))
		mineHash, _ := storage.writeTree([]Entry{
			{Name: "fileA.txt", Mode: 0100644, Size: 15, BlobSHA256: bA},
			{Name: "fileB.txt", Mode: 0100644, Size: 24, BlobSHA256: bB_mod},
			{Name: "fileC.txt", Mode: 0100644, Size: 21, BlobSHA256: bC},
		})

		mergedHash, conflicts, err := ThreeWayTreeMerge(
			ctx, ancHash, theirsHash, mineHash,
			storage.fetchTree, storage.readBlob, storage.writeBlob, storage.writeTree,
			"PARENT", "CHILD",
		)
		if err != nil || len(conflicts) > 0 {
			t.Fatalf("expected clean merge, got error: %v, conflicts: %v", err, conflicts)
		}

		entries, _ := storage.fetchTree(mergedHash)
		if len(entries) != 3 {
			t.Fatalf("expected 3 entries in merged tree, got %d", len(entries))
		}
		if entries[0].Name != "fileA.txt" || !bytes.Equal(entries[0].BlobSHA256, bA_mod) {
			t.Errorf("fileA.txt was not taken from theirs")
		}
		if entries[1].Name != "fileB.txt" || !bytes.Equal(entries[1].BlobSHA256, bB_mod) {
			t.Errorf("fileB.txt was not taken from mine")
		}
		if entries[2].Name != "fileC.txt" || !bytes.Equal(entries[2].BlobSHA256, bC) {
			t.Errorf("fileC.txt was not added from mine")
		}
	})

	t.Run("Overlapping File Modification Conflict", func(t *testing.T) {
		storage := newMemoryStorage()

		// Base tree: fileA
		bA, _ := storage.writeBlob([]byte("line1\nshared line\nline3\n"))
		ancHash, _ := storage.writeTree([]Entry{
			{Name: "fileA.txt", Mode: 0100644, Size: 24, BlobSHA256: bA},
		})

		// Theirs modifies shared line
		bA_theirs, _ := storage.writeBlob([]byte("line1\nparent change\nline3\n"))
		theirsHash, _ := storage.writeTree([]Entry{
			{Name: "fileA.txt", Mode: 0100644, Size: 26, BlobSHA256: bA_theirs},
		})

		// Mine modifies shared line differently
		bA_mine, _ := storage.writeBlob([]byte("line1\nchild change\nline3\n"))
		mineHash, _ := storage.writeTree([]Entry{
			{Name: "fileA.txt", Mode: 0100644, Size: 25, BlobSHA256: bA_mine},
		})

		_, conflicts, err := ThreeWayTreeMerge(
			ctx, ancHash, theirsHash, mineHash,
			storage.fetchTree, storage.readBlob, storage.writeBlob, storage.writeTree,
			"PARENT", "CHILD",
		)
		if err == nil {
			t.Fatalf("expected ErrMergeConflict, got nil error")
		}
		if len(conflicts) != 1 {
			t.Fatalf("expected 1 conflict, got %d", len(conflicts))
		}
		if conflicts[0].Path != "fileA.txt" || conflicts[0].Reason != "content_conflict" {
			t.Errorf("unexpected conflict details: %+v", conflicts[0])
		}
		if !strings.Contains(string(conflicts[0].ConflictedOutput), "<<<<<<< PARENT") {
			t.Errorf("conflicted output missing markers:\n%s", string(conflicts[0].ConflictedOutput))
		}
	})

	t.Run("Nested Subdirectories Merge Cleanly", func(t *testing.T) {
		storage := newMemoryStorage()

		// pkg/auth/token.go
		bToken, _ := storage.writeBlob([]byte("token original\n"))
		subTreeAnc, _ := storage.writeTree([]Entry{
			{Name: "token.go", Mode: 0100644, Size: 15, BlobSHA256: bToken},
		})
		ancHash, _ := storage.writeTree([]Entry{
			{Name: "pkg", Mode: 0040755, SubTreeHash: subTreeAnc},
		})

		// Theirs updates pkg/auth/token.go
		bTokenTheirs, _ := storage.writeBlob([]byte("token updated by parent\n"))
		subTreeTheirs, _ := storage.writeTree([]Entry{
			{Name: "token.go", Mode: 0100644, Size: 24, BlobSHA256: bTokenTheirs},
		})
		theirsHash, _ := storage.writeTree([]Entry{
			{Name: "pkg", Mode: 0040755, SubTreeHash: subTreeTheirs},
		})

		// Mine adds pkg/client.go
		bClient, _ := storage.writeBlob([]byte("client added by child\n"))
		subTreeMine, _ := storage.writeTree([]Entry{
			{Name: "client.go", Mode: 0100644, Size: 22, BlobSHA256: bClient},
			{Name: "token.go", Mode: 0100644, Size: 15, BlobSHA256: bToken},
		})
		mineHash, _ := storage.writeTree([]Entry{
			{Name: "pkg", Mode: 0040755, SubTreeHash: subTreeMine},
		})

		mergedHash, conflicts, err := ThreeWayTreeMerge(
			ctx, ancHash, theirsHash, mineHash,
			storage.fetchTree, storage.readBlob, storage.writeBlob, storage.writeTree,
			"PARENT", "CHILD",
		)
		if err != nil || len(conflicts) > 0 {
			t.Fatalf("expected clean merge of subdirectories, got err: %v, conflicts: %v", err, conflicts)
		}

		topEntries, _ := storage.fetchTree(mergedHash)
		if len(topEntries) != 1 || topEntries[0].Name != "pkg" {
			t.Fatalf("expected 1 top entry 'pkg'")
		}
		pkgEntries, _ := storage.fetchTree(topEntries[0].SubTreeHash)
		if len(pkgEntries) != 2 {
			t.Fatalf("expected 2 entries in pkg, got %d", len(pkgEntries))
		}
		if pkgEntries[0].Name != "client.go" || pkgEntries[1].Name != "token.go" {
			t.Errorf("unexpected pkg entries: %+v", pkgEntries)
		}
		if !bytes.Equal(pkgEntries[1].BlobSHA256, bTokenTheirs) {
			t.Errorf("token.go was not updated from theirs")
		}
	})
}
