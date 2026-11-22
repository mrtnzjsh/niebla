package fs

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"bazil.org/fuse"
	fusefs "bazil.org/fuse/fs"
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

func TestFS_Dir_Mkdir(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	fsInstance := NewFS(scratchDir, cacheDir, nil)
	rootNode, err := fsInstance.Root()
	if err != nil {
		t.Fatalf("failed to get root node: %v", err)
	}
	root := rootNode.(*Dir)

	mkdirReq := &fuse.MkdirRequest{
		Name: "testdir",
		Mode: 0755,
	}
	node, err := root.Mkdir(context.Background(), mkdirReq)
	if err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	dirNode, ok := node.(*Dir)
	if !ok {
		t.Fatalf("expected node to be *Dir, got %T", node)
	}
	if dirNode.path != "testdir" {
		t.Errorf("expected path testdir, got %q", dirNode.path)
	}

	// Verify physical directory in scratch
	info, err := os.Stat(filepath.Join(scratchDir, "testdir"))
	if err != nil {
		t.Fatalf("stat on created scratch dir failed: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("expected testdir to be a directory")
	}

	// Verify Lookup finds it
	lookedUp, err := root.Lookup(context.Background(), "testdir")
	if err != nil {
		t.Fatalf("Lookup testdir failed: %v", err)
	}
	if _, ok := lookedUp.(*Dir); !ok {
		t.Fatalf("expected looked up node to be *Dir, got %T", lookedUp)
	}

	// Verify ReadDirAll includes it
	dirents, err := root.ReadDirAll(context.Background())
	if err != nil {
		t.Fatalf("ReadDirAll failed: %v", err)
	}
	found := false
	for _, de := range dirents {
		if de.Name == "testdir" && de.Type == fuse.DT_Dir {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected testdir in ReadDirAll results, got %v", dirents)
	}
}

func TestFS_Dir_Create(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	fsInstance := NewFS(scratchDir, cacheDir, nil)
	rootNode, _ := fsInstance.Root()
	root := rootNode.(*Dir)

	createReq := &fuse.CreateRequest{
		Name: "created_file.txt",
		Mode: 0644,
	}
	createResp := &fuse.CreateResponse{}
	node, handle, err := root.Create(context.Background(), createReq, createResp)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	fileNode, ok := node.(*File)
	if !ok {
		t.Fatalf("expected *File node, got %T", node)
	}
	if !fileNode.isScratch {
		t.Errorf("expected isScratch to be true")
	}

	// Write through handle
	content := []byte("payload written to created file")
	writeReq := &fuse.WriteRequest{
		Data:   content,
		Offset: 0,
	}
	writeResp := &fuse.WriteResponse{}
	writer := handle.(fusefs.HandleWriter)
	if err := writer.Write(context.Background(), writeReq, writeResp); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Verify content on disk in scratch
	diskBytes, err := os.ReadFile(filepath.Join(scratchDir, "created_file.txt"))
	if err != nil {
		t.Fatalf("failed to read scratch file from disk: %v", err)
	}
	if string(diskBytes) != string(content) {
		t.Errorf("expected disk content %q, got %q", string(content), string(diskBytes))
	}

	// Verify Read from node
	readReq := &fuse.ReadRequest{Offset: 0, Size: len(content)}
	readResp := &fuse.ReadResponse{}
	if err := fileNode.Read(context.Background(), readReq, readResp); err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(readResp.Data) != string(content) {
		t.Errorf("expected read data %q, got %q", string(content), string(readResp.Data))
	}
}

func TestFS_Dir_Remove(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	cache := metadata.NewMetadataCache()
	fsInstance := NewFS(scratchDir, cacheDir, cache)
	rootNode, _ := fsInstance.Root()
	root := rootNode.(*Dir)

	// 1. Scratch file removal
	createReq := &fuse.CreateRequest{Name: "scratch_remove.txt", Mode: 0644}
	_, _, err := root.Create(context.Background(), createReq, &fuse.CreateResponse{})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	remReq := &fuse.RemoveRequest{Name: "scratch_remove.txt", Dir: false}
	if err := root.Remove(context.Background(), remReq); err != nil {
		t.Fatalf("Remove scratch file failed: %v", err)
	}
	_, err = root.Lookup(context.Background(), "scratch_remove.txt")
	if err != syscall.ENOENT {
		t.Errorf("expected ENOENT after remove, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(scratchDir, "scratch_remove.txt")); !os.IsNotExist(err) {
		t.Errorf("expected scratch file to be deleted from disk")
	}

	// 2. Base CAS file removal (creates whiteout)
	blobContent := []byte("base cas entry")
	blobHash := "casblob12345"
	_ = os.WriteFile(filepath.Join(cacheDir, blobHash), blobContent, 0644)
	cache.Set("base_remove.txt", metadata.Metadata{
		Name:       "base_remove.txt",
		ByteSize:   int64(len(blobContent)),
		BlobSHA256: blobHash,
	})

	// Verify it can be looked up initially
	if _, err := root.Lookup(context.Background(), "base_remove.txt"); err != nil {
		t.Fatalf("expected initial lookup to succeed, got %v", err)
	}

	// Remove base CAS file
	remBaseReq := &fuse.RemoveRequest{Name: "base_remove.txt", Dir: false}
	if err := root.Remove(context.Background(), remBaseReq); err != nil {
		t.Fatalf("Remove base file failed: %v", err)
	}

	// Verify Lookup now returns ENOENT
	if _, err := root.Lookup(context.Background(), "base_remove.txt"); err != syscall.ENOENT {
		t.Errorf("expected ENOENT for whiteouted base file, got %v", err)
	}

	// Verify whiteout marker exists on disk
	whiteoutPath := filepath.Join(scratchDir, ".wh.base_remove.txt")
	if _, err := os.Stat(whiteoutPath); err != nil {
		t.Errorf("expected whiteout marker at %s, got err %v", whiteoutPath, err)
	}

	// Verify ReadDirAll does NOT list base_remove.txt nor .wh.base_remove.txt
	dirents, err := root.ReadDirAll(context.Background())
	if err != nil {
		t.Fatalf("ReadDirAll failed: %v", err)
	}
	for _, de := range dirents {
		if de.Name == "base_remove.txt" || de.Name == ".wh.base_remove.txt" {
			t.Errorf("unexpected entry in ReadDirAll: %s", de.Name)
		}
	}
}

func TestFS_Dir_NegativeCacheClearing(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	cache := metadata.NewMetadataCache()
	fsInstance := NewFS(scratchDir, cacheDir, cache)
	rootNode, _ := fsInstance.Root()
	root := rootNode.(*Dir)

	// 1. Initial lookup fails and sets negative cache
	_, err := root.Lookup(context.Background(), "future_file.txt")
	if err != syscall.ENOENT {
		t.Fatalf("expected ENOENT, got %v", err)
	}
	if !cache.IsNegative("future_file.txt") {
		t.Fatalf("expected future_file.txt to be recorded in negative cache")
	}

	// 2. Create the file - must clear negative cache
	createReq := &fuse.CreateRequest{Name: "future_file.txt", Mode: 0644}
	_, _, err = root.Create(context.Background(), createReq, &fuse.CreateResponse{})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if cache.IsNegative("future_file.txt") {
		t.Errorf("expected negative cache entry to be cleared after Create")
	}

	// 3. Lookup immediately succeeds
	node, err := root.Lookup(context.Background(), "future_file.txt")
	if err != nil {
		t.Fatalf("Lookup after Create failed: %v", err)
	}
	if node == nil {
		t.Errorf("expected non-nil node")
	}
}

func TestFS_Dir_Rename(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	cache := metadata.NewMetadataCache()
	fsInstance := NewFS(scratchDir, cacheDir, cache)
	rootNode, _ := fsInstance.Root()
	root := rootNode.(*Dir)

	// 1. Scratch file rename
	createReq := &fuse.CreateRequest{Name: "scratch_old.txt", Mode: 0644}
	node, handle, err := root.Create(context.Background(), createReq, &fuse.CreateResponse{})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	writer := handle.(fusefs.HandleWriter)
	_ = writer.Write(context.Background(), &fuse.WriteRequest{Data: []byte("scratch data")}, &fuse.WriteResponse{})

	renameReq := &fuse.RenameRequest{
		OldName: "scratch_old.txt",
		NewName: "scratch_new.txt",
	}
	if err := root.Rename(context.Background(), renameReq, root); err != nil {
		t.Fatalf("Rename failed: %v", err)
	}

	// Old should be ENOENT
	if _, err := root.Lookup(context.Background(), "scratch_old.txt"); err != syscall.ENOENT {
		t.Errorf("expected ENOENT for old name, got %v", err)
	}

	// New should succeed and have correct data
	newNode, err := root.Lookup(context.Background(), "scratch_new.txt")
	if err != nil {
		t.Fatalf("Lookup scratch_new.txt failed: %v", err)
	}
	newFile := newNode.(*File)
	readResp := &fuse.ReadResponse{}
	if err := newFile.Read(context.Background(), &fuse.ReadRequest{Size: 100}, readResp); err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(readResp.Data) != "scratch data" {
		t.Errorf("expected 'scratch data', got %q", string(readResp.Data))
	}

	// 2. Base CAS file rename
	casData := []byte("base cas to rename")
	casHash := "renameblob999"
	_ = os.WriteFile(filepath.Join(cacheDir, casHash), casData, 0644)
	cache.Set("cas_old.txt", metadata.Metadata{
		Name:       "cas_old.txt",
		ByteSize:   int64(len(casData)),
		BlobSHA256: casHash,
	})

	casRenameReq := &fuse.RenameRequest{
		OldName: "cas_old.txt",
		NewName: "cas_new.txt",
	}
	if err := root.Rename(context.Background(), casRenameReq, root); err != nil {
		t.Fatalf("Rename base CAS file failed: %v", err)
	}

	// Verify old has whiteout marker
	if _, err := os.Stat(filepath.Join(scratchDir, ".wh.cas_old.txt")); err != nil {
		t.Errorf("expected whiteout for old base CAS file: %v", err)
	}
	// Verify old returns ENOENT
	if _, err := root.Lookup(context.Background(), "cas_old.txt"); err != syscall.ENOENT {
		t.Errorf("expected ENOENT for renamed base file, got %v", err)
	}

	// Verify new exists in scratch and has contents
	newCasNode, err := root.Lookup(context.Background(), "cas_new.txt")
	if err != nil {
		t.Fatalf("Lookup cas_new.txt failed: %v", err)
	}
	newCasFile := newCasNode.(*File)
	casReadResp := &fuse.ReadResponse{}
	if err := newCasFile.Read(context.Background(), &fuse.ReadRequest{Size: 100}, casReadResp); err != nil {
		t.Fatalf("Read on renamed file failed: %v", err)
	}
	if string(casReadResp.Data) != string(casData) {
		t.Errorf("expected %q, got %q", string(casData), string(casReadResp.Data))
	}
	_ = node
}

func TestFS_Dir_And_File_Attr_UidGid(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	fsInstance := NewFS(scratchDir, cacheDir, nil)
	rootNode, err := fsInstance.Root()
	if err != nil {
		t.Fatalf("Root failed: %v", err)
	}
	root := rootNode.(*Dir)

	// 1. Root Dir Attr should mirror caller EUID and EGID
	var dirAttr fuse.Attr
	if err := root.Attr(context.Background(), &dirAttr); err != nil {
		t.Fatalf("root.Attr failed: %v", err)
	}
	expectedUID := uint32(os.Geteuid())
	expectedGID := uint32(os.Getegid())
	if dirAttr.Uid != expectedUID {
		t.Errorf("expected dirAttr.Uid == %d, got %d", expectedUID, dirAttr.Uid)
	}
	if dirAttr.Gid != expectedGID {
		t.Errorf("expected dirAttr.Gid == %d, got %d", expectedGID, dirAttr.Gid)
	}

	// 2. Created File Attr should mirror caller EUID and EGID
	createReq := &fuse.CreateRequest{Name: "attr_test.txt", Mode: 0644}
	node, _, err := root.Create(context.Background(), createReq, &fuse.CreateResponse{})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	fileNode := node.(*File)

	var fileAttr fuse.Attr
	if err := fileNode.Attr(context.Background(), &fileAttr); err != nil {
		t.Fatalf("fileNode.Attr failed: %v", err)
	}
	if fileAttr.Uid != expectedUID {
		t.Errorf("expected fileAttr.Uid == %d, got %d", expectedUID, fileAttr.Uid)
	}
	if fileAttr.Gid != expectedGID {
		t.Errorf("expected fileAttr.Gid == %d, got %d", expectedGID, fileAttr.Gid)
	}
}

func TestFS_Dir_Remove_NonEmptyScratch_ReturnsENOTEMPTY(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	cache := metadata.NewMetadataCache()
	fsInstance := NewFS(scratchDir, cacheDir, cache)
	rootNode, err := fsInstance.Root()
	if err != nil {
		t.Fatalf("Root failed: %v", err)
	}
	root := rootNode.(*Dir)

	// 1. Create a directory in scratch
	dirNode, err := root.Mkdir(context.Background(), &fuse.MkdirRequest{Name: "my_dir", Mode: 0755})
	if err != nil {
		t.Fatalf("Mkdir failed: %v", err)
	}
	dir := dirNode.(*Dir)

	// 2. Create a child file inside that directory
	_, _, err = dir.Create(context.Background(), &fuse.CreateRequest{Name: "child.txt", Mode: 0644}, &fuse.CreateResponse{})
	if err != nil {
		t.Fatalf("Create child file failed: %v", err)
	}

	// 3. Attempt to remove the non-empty directory -> must return syscall.ENOTEMPTY, NOT EIO
	err = root.Remove(context.Background(), &fuse.RemoveRequest{Name: "my_dir", Dir: true})
	if err != syscall.ENOTEMPTY {
		t.Fatalf("expected syscall.ENOTEMPTY when removing non-empty scratch dir, got %v", err)
	}

	// 4. Remove the child file
	if err := dir.Remove(context.Background(), &fuse.RemoveRequest{Name: "child.txt", Dir: false}); err != nil {
		t.Fatalf("Remove child file failed: %v", err)
	}

	// 5. Attempt to remove the now-empty directory -> must succeed
	if err := root.Remove(context.Background(), &fuse.RemoveRequest{Name: "my_dir", Dir: true}); err != nil {
		t.Fatalf("expected removing empty scratch dir to succeed, got %v", err)
	}

	// 6. Verify directory no longer exists in scratch
	if _, err := os.Stat(filepath.Join(scratchDir, "my_dir")); !os.IsNotExist(err) {
		t.Errorf("expected scratch my_dir to be removed, got err: %v", err)
	}
}

func TestFS_Dir_Remove_NonEmptyBase_ReturnsENOTEMPTY(t *testing.T) {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	cache := metadata.NewMetadataCache()
	// Set up base directory and base child file in cache
	cache.Set("base_folder", metadata.Metadata{
		Name: "base_folder",
		Mode: syscall.S_IFDIR | 0755,
	})
	cache.Set("base_folder/child.txt", metadata.Metadata{
		Name:     "base_folder/child.txt",
		Mode:     0644,
		ByteSize: 10,
	})

	fsInstance := NewFS(scratchDir, cacheDir, cache)
	rootNode, err := fsInstance.Root()
	if err != nil {
		t.Fatalf("Root failed: %v", err)
	}
	root := rootNode.(*Dir)

	// 1. Attempt to remove non-empty base directory -> must return syscall.ENOTEMPTY
	err = root.Remove(context.Background(), &fuse.RemoveRequest{Name: "base_folder", Dir: true})
	if err != syscall.ENOTEMPTY {
		t.Fatalf("expected syscall.ENOTEMPTY for non-empty base dir, got %v", err)
	}

	// 2. Lookup child folder node
	childDirNode, err := root.Lookup(context.Background(), "base_folder")
	if err != nil {
		t.Fatalf("Lookup base_folder failed: %v", err)
	}
	childDir := childDirNode.(*Dir)

	// 3. Remove the base child file (creates whiteout marker)
	if err := childDir.Remove(context.Background(), &fuse.RemoveRequest{Name: "child.txt", Dir: false}); err != nil {
		t.Fatalf("Remove base child file failed: %v", err)
	}

	// 4. Now remove base directory -> must succeed
	if err := root.Remove(context.Background(), &fuse.RemoveRequest{Name: "base_folder", Dir: true}); err != nil {
		t.Fatalf("expected removing whiteouted base dir to succeed, got %v", err)
	}

	// 5. Whiteout for the folder itself must be written
	if _, err := os.Stat(filepath.Join(scratchDir, ".wh.base_folder")); err != nil {
		t.Errorf("expected whiteout marker .wh.base_folder, got err: %v", err)
	}
}
