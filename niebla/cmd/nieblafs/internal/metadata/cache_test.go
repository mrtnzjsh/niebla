package metadata

import (
	"testing"
	"time"

	"niebla/pkg/tree"
)

func TestMetadataCache_Operations(t *testing.T) {
	cache := NewMetadataCache()

	if count := cache.Count(); count != 0 {
		t.Fatalf("expected initial count 0, got %d", count)
	}

	cache.Set("src/main.go", Metadata{
		Name:       "main.go",
		Mode:       0100644,
		ByteSize:   128,
		BlobSHA256: "abc123",
	})
	cache.Set("README.md", Metadata{
		Name:       "README.md",
		Mode:       0100644,
		ByteSize:   50,
		BlobSHA256: "def456",
	})

	if count := cache.Count(); count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}

	meta, ok := cache.Get("src/main.go")
	if !ok || meta.ByteSize != 128 {
		t.Fatalf("expected to find src/main.go with size 128, got ok=%v, meta=%+v", ok, meta)
	}

	// Invalidate single path
	if !cache.Invalidate("src/main.go") {
		t.Fatalf("expected Invalidate to return true for existing path")
	}
	if cache.Invalidate("nonexistent") {
		t.Fatalf("expected Invalidate to return false for nonexistent path")
	}
	if count := cache.Count(); count != 1 {
		t.Fatalf("expected count 1 after invalidation, got %d", count)
	}

	// Reanchor
	cache.SetRootTreeHash("old-hash")
	cache.SetCLID(10)
	purged := cache.Reanchor("new-hash", 15)
	if purged != 1 {
		t.Fatalf("expected 1 entry purged on reanchor, got %d", purged)
	}
	if cache.Count() != 0 {
		t.Fatalf("expected 0 entries after reanchor, got %d", cache.Count())
	}
	if cache.RootTreeHash() != "new-hash" {
		t.Fatalf("expected RootTreeHash 'new-hash', got %q", cache.RootTreeHash())
	}
	if cache.CLID() != 15 {
		t.Fatalf("expected CLID 15, got %d", cache.CLID())
	}
}

func TestMetadataCache_ListDir(t *testing.T) {
	cache := NewMetadataCache()
	cache.Set("README.md", Metadata{Name: "README.md"})
	cache.Set("src/main.go", Metadata{Name: "main.go"})
	cache.Set("src/util.go", Metadata{Name: "util.go"})
	cache.Set("src/sub/deep.go", Metadata{Name: "deep.go"})

	rootEntries := cache.ListDir("")
	if len(rootEntries) != 1 || rootEntries[0].Name != "README.md" {
		t.Errorf("expected 1 root entry (README.md), got %v", rootEntries)
	}

	srcEntries := cache.ListDir("src")
	if len(srcEntries) != 2 {
		t.Errorf("expected 2 src entries, got %d", len(srcEntries))
	}

	subEntries := cache.ListDir("src/sub")
	if len(subEntries) != 1 || subEntries[0].Name != "deep.go" {
		t.Errorf("expected 1 sub entry (deep.go), got %v", subEntries)
	}
}

func TestMetadataCache_PopulateAndGetSubtreeHash(t *testing.T) {
	cache := NewMetadataCache()
	cache.SetRootTreeHash("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	if cache.IsDirectoryLoaded("") {
		t.Errorf("root directory should not be loaded initially")
	}

	rootHash, ok := cache.GetSubtreeHash("")
	if !ok || len(rootHash) != 32 {
		t.Fatalf("expected to get 32-byte root tree hash, ok=%v", ok)
	}

	subTreeHash := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}
	blobHash := []byte{32, 31, 30, 29, 28, 27, 26, 25, 24, 23, 22, 21, 20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1}

	// Populate root
	cache.PopulateDir("", []tree.Entry{
		{Name: "README.md", Size: 42, BlobSHA256: blobHash, Mode: 0100644},
		{Name: "src", SubTreeHash: subTreeHash, Mode: 0040755},
	})

	if !cache.IsDirectoryLoaded("") {
		t.Errorf("root directory should be marked as loaded")
	}

	subH, ok := cache.GetSubtreeHash("src")
	if !ok || len(subH) != 32 {
		t.Fatalf("expected to get src subtree hash, ok=%v", ok)
	}

	// Populate src
	cache.PopulateDir("src", []tree.Entry{
		{Name: "main.go", Size: 100, BlobSHA256: blobHash, Mode: 0100644},
	})

	if !cache.IsDirectoryLoaded("src") {
		t.Errorf("src directory should be marked as loaded")
	}

	meta, ok := cache.Get("src/main.go")
	if !ok || meta.ByteSize != 100 {
		t.Errorf("expected src/main.go in cache with size 100, got ok=%v, meta=%+v", ok, meta)
	}
}

func TestMetadataCache_BoundedLRUEviction(t *testing.T) {
	// Cache capacity = 3 entries
	cache := NewMetadataCacheWithLimit(3)

	cache.Set("file1", Metadata{Name: "file1"})
	cache.Set("file2", Metadata{Name: "file2"})
	cache.Set("file3", Metadata{Name: "file3"})

	if cache.Count() != 3 {
		t.Fatalf("expected count 3, got %d", cache.Count())
	}

	// Access file1 to make it most recently used (LRU order: file2, file3, file1)
	if _, ok := cache.Get("file1"); !ok {
		t.Fatalf("file1 should exist")
	}

	// Add file4 -> should evict file2 (least recently used)
	cache.Set("file4", Metadata{Name: "file4"})

	if cache.Count() != 3 {
		t.Fatalf("expected count capped at 3, got %d", cache.Count())
	}

	if _, ok := cache.Get("file2"); ok {
		t.Errorf("file2 should have been evicted by LRU")
	}
	if _, ok := cache.Get("file1"); !ok {
		t.Errorf("file1 should still exist (was accessed)")
	}
	if _, ok := cache.Get("file3"); !ok {
		t.Errorf("file3 should still exist")
	}
	if _, ok := cache.Get("file4"); !ok {
		t.Errorf("file4 should still exist")
	}
}

func TestMetadataCache_NegativeLookup(t *testing.T) {
	cache := NewMetadataCache()

	if cache.IsNegative("missing.txt") {
		t.Errorf("missing.txt should not be negative initially")
	}

	// Cache negative lookup with 50ms TTL
	cache.SetNegative("missing.txt", 50*time.Millisecond)

	if !cache.IsNegative("missing.txt") {
		t.Errorf("missing.txt should be negative hit within TTL")
	}

	// Invalidate or ClearNegative
	cache.ClearNegative("missing.txt")
	if cache.IsNegative("missing.txt") {
		t.Errorf("missing.txt should not be negative after ClearNegative")
	}
}
