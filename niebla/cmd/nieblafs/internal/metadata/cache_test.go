package metadata

import (
	"testing"
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
