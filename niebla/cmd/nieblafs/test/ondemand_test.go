package test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"bazil.org/fuse"
	"niebla/cmd/nieblafs/internal/fs"
	"niebla/cmd/nieblafs/internal/metadata"
	"niebla/cmd/nieblafs/internal/remote"
	"niebla/pkg/cas"
	"niebla/pkg/tree"
)

type testMockRemote struct {
	blobs           map[string][]byte
	trees           map[string][]tree.Entry
	fetchTreeCalls  atomic.Int64
	streamBlobCalls atomic.Int64
}

func (m *testMockRemote) FetchTree(ctx context.Context, treeHash []byte) ([]tree.Entry, error) {
	m.fetchTreeCalls.Add(1)
	key := hex.EncodeToString(treeHash)
	entries, ok := m.trees[key]
	if !ok {
		return nil, fmt.Errorf("tree %s not found", key)
	}
	return entries, nil
}

func (m *testMockRemote) FetchTreePaged(ctx context.Context, treeHash []byte, pageSize int32, onPage func([]tree.Entry) error) error {
	entries, err := m.FetchTree(ctx, treeHash)
	if err != nil {
		return err
	}
	return onPage(entries)
}

func (m *testMockRemote) StreamBlob(ctx context.Context, sha256Bytes []byte, dst io.Writer) error {
	m.streamBlobCalls.Add(1)
	key := hex.EncodeToString(sha256Bytes)
	data, ok := m.blobs[key]
	if !ok {
		return fmt.Errorf("blob %s not found", key)
	}
	_, err := dst.Write(data)
	return err
}

func (m *testMockRemote) Close() error {
	return nil
}

type onDemandTestHarness struct {
	scratchDir    string
	cacheDir      string
	readmeContent []byte
	readmeHash    [32]byte
	mainContent   []byte
	mainHash      [32]byte
	mockClient    *testMockRemote
	fsInstance    *fs.FS
	rootDir       *fs.Dir
}

func setupOnDemandTestHarness(t *testing.T) *onDemandTestHarness {
	tmpDir := t.TempDir()
	scratchDir := filepath.Join(tmpDir, "scratch")
	cacheDir := filepath.Join(tmpDir, "cache")
	_ = os.MkdirAll(scratchDir, 0755)
	_ = os.MkdirAll(cacheDir, 0755)

	casStorage, err := cas.NewStorage(cacheDir)
	if err != nil {
		t.Fatalf("failed to init CAS storage: %v", err)
	}

	readmeContent := []byte("# Virtual Monorepo\nThis is lazily loaded.")
	readmeHash := sha256.Sum256(readmeContent)

	mainContent := []byte("package main\nfunc main() {}\n")
	mainHash := sha256.Sum256(mainContent)

	srcSubtreeHash := []byte{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160, 170, 180, 190, 200, 210, 220, 230, 240, 250, 1, 2, 3, 4, 5, 6, 7}
	rootTreeHash := []byte{99, 88, 77, 66, 55, 44, 33, 22, 11, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22}

	mockClient := &testMockRemote{
		blobs: map[string][]byte{
			hex.EncodeToString(readmeHash[:]): readmeContent,
			hex.EncodeToString(mainHash[:]):   mainContent,
		},
		trees: map[string][]tree.Entry{
			hex.EncodeToString(rootTreeHash): {
				{Name: "README.md", Size: uint64(len(readmeContent)), BlobSHA256: readmeHash[:], Mode: 0100644},
				{Name: "src", SubTreeHash: srcSubtreeHash, Mode: 0040755},
			},
			hex.EncodeToString(srcSubtreeHash): {
				{Name: "main.go", Size: uint64(len(mainContent)), BlobSHA256: mainHash[:], Mode: 0100644},
			},
		},
	}

	fetcher := remote.NewBlobFetcher(casStorage, mockClient)
	metaCache := metadata.NewMetadataCache()
	metaCache.SetRootTreeHash(hex.EncodeToString(rootTreeHash))

	fsInstance := fs.NewFS(
		scratchDir, cacheDir, metaCache,
		fs.WithCASStorage(casStorage),
		fs.WithRemoteClient(mockClient),
		fs.WithFetcher(fetcher),
	)

	rootNode, err := fsInstance.Root()
	if err != nil {
		t.Fatalf("fsInstance.Root failed: %v", err)
	}
	rootDir, ok := rootNode.(*fs.Dir)
	if !ok {
		t.Fatalf("expected *fs.Dir for root node")
	}

	return &onDemandTestHarness{
		scratchDir:    scratchDir,
		cacheDir:      cacheDir,
		readmeContent: readmeContent,
		readmeHash:    readmeHash,
		mainContent:   mainContent,
		mainHash:      mainHash,
		mockClient:    mockClient,
		fsInstance:    fsInstance,
		rootDir:       rootDir,
	}
}

func TestOnDemand_LazyDirectoryTraversal(t *testing.T) {
	h := setupOnDemandTestHarness(t)
	ctx := context.Background()

	// Root directory ReadDirAll should lazily trigger FetchTree for root
	rootEntries, err := h.rootDir.ReadDirAll(ctx)
	if err != nil {
		t.Fatalf("ReadDirAll failed: %v", err)
	}
	if h.mockClient.fetchTreeCalls.Load() != 1 {
		t.Errorf("expected 1 FetchTree call, got %d", h.mockClient.fetchTreeCalls.Load())
	}
	if len(rootEntries) != 2 {
		t.Fatalf("expected 2 root entries, got %d", len(rootEntries))
	}

	// Subdirectory traversal should lazily trigger FetchTree for src
	srcNode, err := h.rootDir.Lookup(ctx, "src")
	if err != nil {
		t.Fatalf("Lookup('src') failed: %v", err)
	}
	srcDir := srcNode.(*fs.Dir)
	srcEntries, err := srcDir.ReadDirAll(ctx)
	if err != nil {
		t.Fatalf("srcDir.ReadDirAll failed: %v", err)
	}
	if h.mockClient.fetchTreeCalls.Load() != 2 {
		t.Errorf("expected 2 FetchTree calls, got %d", h.mockClient.fetchTreeCalls.Load())
	}
	if len(srcEntries) != 1 || srcEntries[0].Name != "main.go" {
		t.Fatalf("expected main.go in src, got %v", srcEntries)
	}
}

func TestOnDemand_LazyBlobStreamingAndCaching(t *testing.T) {
	h := setupOnDemandTestHarness(t)
	ctx := context.Background()

	readmeNode, err := h.rootDir.Lookup(ctx, "README.md")
	if err != nil {
		t.Fatalf("Lookup('README.md') failed: %v", err)
	}
	readmeFile := readmeNode.(*fs.File)

	// First read streams blob and caches locally
	readReq := &fuse.ReadRequest{Offset: 0, Size: len(h.readmeContent)}
	readResp := &fuse.ReadResponse{}
	if err := readmeFile.Read(ctx, readReq, readResp); err != nil {
		t.Fatalf("readmeFile.Read failed: %v", err)
	}
	if h.mockClient.streamBlobCalls.Load() != 1 {
		t.Errorf("expected 1 StreamBlob call, got %d", h.mockClient.streamBlobCalls.Load())
	}
	if !bytes.Equal(readResp.Data, h.readmeContent) {
		t.Errorf("read mismatch: got %q, want %q", string(readResp.Data), string(h.readmeContent))
	}

	// Second read hits local CAS cache
	readResp2 := &fuse.ReadResponse{}
	if err := readmeFile.Read(ctx, readReq, readResp2); err != nil {
		t.Fatalf("second read failed: %v", err)
	}
	if h.mockClient.streamBlobCalls.Load() != 1 {
		t.Errorf("expected stream calls to remain 1, got %d", h.mockClient.streamBlobCalls.Load())
	}
}

func TestOnDemand_CopyOnWriteMutation(t *testing.T) {
	h := setupOnDemandTestHarness(t)
	ctx := context.Background()

	readmeNode, err := h.rootDir.Lookup(ctx, "README.md")
	if err != nil {
		t.Fatalf("Lookup('README.md') failed: %v", err)
	}
	readmeFile := readmeNode.(*fs.File)

	newContent := []byte("# Modified in VS Code")
	// Simulate editor truncate (Setattr) followed by Write
	setattrReq := &fuse.SetattrRequest{
		Valid: fuse.SetattrSize,
		Size:  uint64(len(newContent)),
	}
	setattrResp := &fuse.SetattrResponse{}
	if err := readmeFile.Setattr(ctx, setattrReq, setattrResp); err != nil {
		t.Fatalf("readmeFile.Setattr failed: %v", err)
	}

	writeReq := &fuse.WriteRequest{Offset: 0, Data: newContent}
	writeResp := &fuse.WriteResponse{}
	if err := readmeFile.Write(ctx, writeReq, writeResp); err != nil {
		t.Fatalf("readmeFile.Write failed: %v", err)
	}
	if writeResp.Size != len(newContent) {
		t.Errorf("expected write size %d, got %d", len(newContent), writeResp.Size)
	}

	// Verify scratch file created
	scratchFilePath := filepath.Join(h.scratchDir, "README.md")
	scratchData, err := os.ReadFile(scratchFilePath)
	if err != nil {
		t.Fatalf("failed to read scratch file: %v", err)
	}
	if !bytes.Equal(scratchData, newContent) {
		t.Errorf("scratch mismatch: got %q, want %q", string(scratchData), string(newContent))
	}

	// Verify subsequent read returns scratch version
	readResp := &fuse.ReadResponse{}
	readReq := &fuse.ReadRequest{Offset: 0, Size: len(newContent)}
	if err := readmeFile.Read(ctx, readReq, readResp); err != nil {
		t.Fatalf("read after write failed: %v", err)
	}
	if !bytes.Equal(readResp.Data, newContent) {
		t.Errorf("expected modified content from scratch, got %q", string(readResp.Data))
	}
}
