package cli

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"citc-toolchain/pkg/tree"
)

func TestBuildUpdatedTree(t *testing.T) {
	// 1. Initial base tree with one file: "base.txt"
	baseContent := []byte("hello base")
	baseSHA := sha256.Sum256(baseContent)
	baseEntries := []tree.Entry{
		{
			Name:       "base.txt",
			Mode:       0100644,
			Size:       uint64(len(baseContent)),
			BlobSHA256: baseSHA[:],
		},
	}
	baseRootHash := tree.ComputeTreeHash(baseEntries)

	// Tree fetcher mock
	trees := map[string][]tree.Entry{
		string(baseRootHash): baseEntries,
	}
	mockFetch := func(th []byte) ([]tree.Entry, error) {
		if e, ok := trees[string(th)]; ok {
			return e, nil
		}
		return []tree.Entry{}, nil
	}

	// 2. Changes:
	// - modify "base.txt"
	// - add "new.txt"
	// - add "sub/nested.txt"
	modContent := []byte("hello base modified")
	modSHA := sha256.Sum256(modContent)

	newContent := []byte("brand new file")
	newSHA := sha256.Sum256(newContent)

	nestedContent := []byte("nested file content")
	nestedSHA := sha256.Sum256(nestedContent)

	changes := []FileChange{
		{
			Path: "base.txt",
			Type: ChangeModified,
		},
		{
			Path: "new.txt",
			Type: ChangeAdded,
		},
		{
			Path: "sub/nested.txt",
			Type: ChangeAdded,
		},
	}

	uploads := map[string]uploadedBlob{
		"base.txt": {
			SHA:  modSHA[:],
			Size: uint64(len(modContent)),
			Mode: 0100644,
		},
		"new.txt": {
			SHA:  newSHA[:],
			Size: uint64(len(newContent)),
			Mode: 0100644,
		},
		"sub/nested.txt": {
			SHA:  nestedSHA[:],
			Size: uint64(len(nestedContent)),
			Mode: 0100644,
		},
	}

	newRootHash, err := BuildUpdatedTree(baseRootHash, changes, uploads, mockFetch)
	if err != nil {
		t.Fatalf("BuildUpdatedTree failed: %v", err)
	}

	if bytes.Equal(newRootHash, baseRootHash) {
		t.Fatalf("expected newRootHash to be different from baseRootHash")
	}

	// 3. Verify tree structure manually
	expectedSubEntries := []tree.Entry{
		{
			Name:       "nested.txt",
			Mode:       0100644,
			Size:       uint64(len(nestedContent)),
			BlobSHA256: nestedSHA[:],
		},
	}
	expectedSubHash := tree.ComputeTreeHash(expectedSubEntries)

	expectedRootEntries := []tree.Entry{
		{
			Name:       "base.txt",
			Mode:       0100644,
			Size:       uint64(len(modContent)),
			BlobSHA256: modSHA[:],
		},
		{
			Name:       "new.txt",
			Mode:       0100644,
			Size:       uint64(len(newContent)),
			BlobSHA256: newSHA[:],
		},
		{
			Name:        "sub",
			Mode:        0040755,
			SubTreeHash: expectedSubHash,
		},
	}
	expectedRootHash := tree.ComputeTreeHash(expectedRootEntries)

	if !bytes.Equal(newRootHash, expectedRootHash) {
		t.Errorf("expected root hash %x, got %x", expectedRootHash, newRootHash)
	}

	// 4. Test Deletion
	delChanges := []FileChange{
		{
			Path: "new.txt",
			Type: ChangeDeleted,
		},
		{
			Path: "sub/nested.txt",
			Type: ChangeDeleted,
		},
	}

	// Add newRootHash and subHash to mock fetcher
	trees[string(newRootHash)] = expectedRootEntries
	trees[string(expectedSubHash)] = expectedSubEntries

	delRootHash, err := BuildUpdatedTree(newRootHash, delChanges, uploads, mockFetch)
	if err != nil {
		t.Fatalf("deletion BuildUpdatedTree failed: %v", err)
	}

	// After deleting "new.txt" and "sub/nested.txt", "sub" directory is empty and removed.
	// Only "base.txt" remains.
	onlyBaseEntries := []tree.Entry{
		{
			Name:       "base.txt",
			Mode:       0100644,
			Size:       uint64(len(modContent)),
			BlobSHA256: modSHA[:],
		},
	}
	expectedDelRootHash := tree.ComputeTreeHash(onlyBaseEntries)
	if !bytes.Equal(delRootHash, expectedDelRootHash) {
		t.Errorf("expected after deletion root hash %x, got %x", expectedDelRootHash, delRootHash)
	}
}
