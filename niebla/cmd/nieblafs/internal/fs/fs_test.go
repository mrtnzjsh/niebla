package fs

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"bazil.org/fuse"
	"niebla/cmd/nieblafs/internal/metadata"
)

func TestFS_LookupAndRead_ScratchPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	cache := metadata.NewMetadataCache()

	// 1. Populate CAS cache blob for "foo.txt"
	casContent := []byte("base cas content")
	blobHash := "abcd1234blob"
	_ = os.WriteFile(filepath.Join(cacheDir, blobHash), casContent, 0644)
	cache.Set("foo.txt", metadata.Metadata{
		Name:       "foo.txt",
		ByteSize:   int64(len(casContent)),
		BlobSHA256: blobHash,
	})

	// 2. Also put modified version in scratch for "foo.txt"
	scratchContent := []byte("scratch override content")
	_ = os.WriteFile(filepath.Join(scratchDir, "foo.txt"), scratchContent, 0644)

	fsInstance := NewFS(scratchDir, cacheDir, cache)
	rootNode, err := fsInstance.Root()
	if err != nil {
		t.Fatalf("failed to get root node: %v", err)
	}

	root := rootNode.(*Dir)

	// Lookup "foo.txt" - MUST resolve scratch version
	node, err := root.Lookup(context.Background(), "foo.txt")
	if err != nil {
		t.Fatalf("lookup foo.txt failed: %v", err)
	}

	fileNode, ok := node.(*File)
	if !ok {
		t.Fatalf("expected node to be *File, got %T", node)
	}
	if !fileNode.isScratch {
		t.Errorf("expected fileNode.isScratch to be true")
	}

	// Read content
	readReq := &fuse.ReadRequest{
		Offset: 0,
		Size:   1024,
	}
	readResp := &fuse.ReadResponse{}
	if err := fileNode.Read(context.Background(), readReq, readResp); err != nil {
		t.Fatalf("failed to read from file: %v", err)
	}
	if string(readResp.Data) != string(scratchContent) {
		t.Errorf("expected scratch content %q, got %q", string(scratchContent), string(readResp.Data))
	}
}

func TestFS_LookupAndRead_CASBlobFallback(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	cache := metadata.NewMetadataCache()

	// Base CAS blob only (not in scratch)
	casContent := []byte("pure cas blob data")
	blobHash := "deadbeef9999"
	_ = os.WriteFile(filepath.Join(cacheDir, blobHash), casContent, 0644)
	cache.Set("bar.txt", metadata.Metadata{
		Name:       "bar.txt",
		ByteSize:   int64(len(casContent)),
		BlobSHA256: blobHash,
	})

	fsInstance := NewFS(scratchDir, cacheDir, cache)
	rootNode, _ := fsInstance.Root()
	root := rootNode.(*Dir)

	node, err := root.Lookup(context.Background(), "bar.txt")
	if err != nil {
		t.Fatalf("lookup bar.txt failed: %v", err)
	}

	fileNode := node.(*File)
	if fileNode.isScratch {
		t.Errorf("expected isScratch to be false for CAS blob fallback")
	}

	readReq := &fuse.ReadRequest{Offset: 0, Size: 1024}
	readResp := &fuse.ReadResponse{}
	if err := fileNode.Read(context.Background(), readReq, readResp); err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(readResp.Data) != string(casContent) {
		t.Errorf("expected cas content %q, got %q", string(casContent), string(readResp.Data))
	}
}

func TestFS_Lookup_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	fsInstance := NewFS(filepath.Join(tmpDir, "scratch"), filepath.Join(tmpDir, "cache"), nil)
	rootNode, _ := fsInstance.Root()
	root := rootNode.(*Dir)

	_, err := root.Lookup(context.Background(), "nonexistent.go")
	if err != syscall.ENOENT {
		t.Errorf("expected syscall.ENOENT, got %v", err)
	}
}

func TestFS_FileWrite_CoWPromotion(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	cache := metadata.NewMetadataCache()
	origContent := []byte("original unedited content")
	blobHash := "origblob123"
	_ = os.WriteFile(filepath.Join(cacheDir, blobHash), origContent, 0644)
	cache.Set("editme.txt", metadata.Metadata{
		Name:       "editme.txt",
		ByteSize:   int64(len(origContent)),
		BlobSHA256: blobHash,
	})

	fsInstance := NewFS(scratchDir, cacheDir, cache)
	rootNode, _ := fsInstance.Root()
	root := rootNode.(*Dir)

	node, err := root.Lookup(context.Background(), "editme.txt")
	if err != nil {
		t.Fatalf("lookup failed: %v", err)
	}
	fileNode := node.(*File)

	// Write to the file: should promote to scratch
	newContent := []byte("overwritten via CoW write")
	writeReq := &fuse.WriteRequest{
		Data:   newContent,
		Offset: 0,
	}
	writeResp := &fuse.WriteResponse{}
	if err := fileNode.Write(context.Background(), writeReq, writeResp); err != nil {
		t.Fatalf("file write failed: %v", err)
	}
	if writeResp.Size != len(newContent) {
		t.Errorf("expected written size %d, got %d", len(newContent), writeResp.Size)
	}

	// Verify scratch file was created on disk
	expectedScratchPath := filepath.Join(scratchDir, "editme.txt")
	scratchBytes, err := os.ReadFile(expectedScratchPath)
	if err != nil {
		t.Fatalf("failed to read promoted scratch file: %v", err)
	}
	if string(scratchBytes) != string(newContent) {
		t.Errorf("expected scratch file content %q, got %q", string(newContent), string(scratchBytes))
	}
}

func TestFS_ReadDirAll_MergedDeduplicated(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	// Scratch has file1 and file2
	_ = os.WriteFile(filepath.Join(scratchDir, "file1.txt"), []byte("1"), 0644)
	_ = os.WriteFile(filepath.Join(scratchDir, "file2.txt"), []byte("2"), 0644)

	// Cache has file2 and file3
	cache := metadata.NewMetadataCache()
	cache.Set("file2.txt", metadata.Metadata{Name: "file2.txt"})
	cache.Set("file3.txt", metadata.Metadata{Name: "file3.txt"})

	fsInstance := NewFS(scratchDir, cacheDir, cache)
	rootNode, _ := fsInstance.Root()
	root := rootNode.(*Dir)

	dirents, err := root.ReadDirAll(context.Background())
	if err != nil {
		t.Fatalf("ReadDirAll failed: %v", err)
	}

	names := make(map[string]bool)
	for _, de := range dirents {
		names[de.Name] = true
	}

	if len(names) != 3 {
		t.Errorf("expected 3 distinct names, got %d (%v)", len(names), names)
	}
	if !names["file1.txt"] || !names["file2.txt"] || !names["file3.txt"] {
		t.Errorf("missing expected entries in ReadDirAll result: %v", names)
	}
}
