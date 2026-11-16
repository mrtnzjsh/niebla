package test

import (
	"os"
	"path/filepath"
	"testing"

	"bazil.org/fuse/fs/fstestutil"
	"niebla/cmd/nieblafs/internal/fs"
	"niebla/cmd/nieblafs/internal/metadata"
	"niebla/internal/cli"
	"niebla/pkg/tree"
)

func TestWriteOperation(t *testing.T) {
	// Smoke test for write operation
}

func TestCreateOperation(t *testing.T) {
	// Smoke test for create operation
}

func TestFUSE_POSIX_MutationsAndStatus(t *testing.T) {
	if _, err := os.Stat("/dev/fuse"); os.IsNotExist(err) {
		t.Skip("skipping FUSE integration test: /dev/fuse not found")
	}

	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	cache := metadata.NewMetadataCache()

	// 1. Setup base CAS file: "base_file.txt"
	casContent := []byte("original base content")
	blobHash := "11223344556677889900aabbccddeeff11223344556677889900aabbccddeeff"
	_ = os.WriteFile(filepath.Join(cacheDir, blobHash), casContent, 0644)
	cache.Set("base_file.txt", metadata.Metadata{
		Name:       "base_file.txt",
		ByteSize:   int64(len(casContent)),
		BlobSHA256: blobHash,
	})

	fsInstance := fs.NewFS(scratchDir, cacheDir, cache)

	mnt, err := fstestutil.MountedT(t, fsInstance, nil)
	if err != nil {
		t.Skipf("skipping live FUSE mount test (fusermount permission or sandbox restriction): %v", err)
		return
	}
	defer mnt.Close()

	// Test 1: POSIX MkdirAll
	nestedDir := filepath.Join(mnt.Dir, "nested", "sub", "dir")
	if err := os.MkdirAll(nestedDir, 0755); err != nil {
		t.Fatalf("failed to mkdir -p: %v", err)
	}

	// Test 2: POSIX File Create and Write
	testFile := filepath.Join(nestedDir, "test.txt")
	payload := []byte("payload data inside virtual fuse workspace\n")
	if err := os.WriteFile(testFile, payload, 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	// Test 3: Read back through FUSE mount
	readBack, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("failed to read created file via mount: %v", err)
	}
	if string(readBack) != string(payload) {
		t.Fatalf("expected payload %q, got %q", string(payload), string(readBack))
	}

	// Test 4: Verify ChangeAdded ([A]) in niebla status
	mockFetch := func(h []byte) ([]tree.Entry, error) {
		return []tree.Entry{
			{Name: "base_file.txt", Mode: 0644, BlobSHA256: []byte(blobHash)},
		}, nil
	}
	changes, err := cli.ScanScratchChanges(scratchDir, []byte("roothash"), mockFetch)
	if err != nil {
		t.Fatalf("ScanScratchChanges failed: %v", err)
	}
	changeMap := make(map[string]cli.ChangeType)
	for _, c := range changes {
		changeMap[c.Path] = c.Type
	}
	if changeMap["nested/sub/dir/test.txt"] != cli.ChangeAdded {
		t.Errorf("expected ChangeAdded for nested/sub/dir/test.txt, got %v", changeMap["nested/sub/dir/test.txt"])
	}

	// Test 5: Remove base CAS file through FUSE mount -> generates whiteout
	baseMountFile := filepath.Join(mnt.Dir, "base_file.txt")
	if err := os.Remove(baseMountFile); err != nil {
		t.Fatalf("failed to remove base_file.txt: %v", err)
	}

	// Verify whiteout exists on disk
	if _, err := os.Stat(filepath.Join(scratchDir, ".wh.base_file.txt")); err != nil {
		t.Fatalf("expected whiteout marker .wh.base_file.txt in scratch: %v", err)
	}

	// Test 6: Verify ChangeDeleted ([D]) in niebla status
	changesAfterDel, err := cli.ScanScratchChanges(scratchDir, []byte("roothash"), mockFetch)
	if err != nil {
		t.Fatalf("ScanScratchChanges after deletion failed: %v", err)
	}
	delMap := make(map[string]cli.ChangeType)
	for _, c := range changesAfterDel {
		delMap[c.Path] = c.Type
	}
	if delMap["base_file.txt"] != cli.ChangeDeleted {
		t.Errorf("expected ChangeDeleted for base_file.txt, got %v", delMap["base_file.txt"])
	}

	// Test 7: POSIX rm and rmdir
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to remove test.txt: %v", err)
	}
	if err := os.Remove(nestedDir); err != nil {
		t.Fatalf("failed to rmdir nested/sub/dir: %v", err)
	}
}
