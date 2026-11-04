package test

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync/atomic"
	"testing"

	"niebla/cmd/nieblafs/internal/fs"
	"niebla/cmd/nieblafs/internal/metadata"
	"niebla/pkg/tree"
)

type pagedMockRemote struct {
	totalEntries int
	pageSize     int32
	callCount    atomic.Int64
}

func (m *pagedMockRemote) FetchTree(ctx context.Context, treeHash []byte) ([]tree.Entry, error) {
	var all []tree.Entry
	err := m.FetchTreePaged(ctx, treeHash, m.pageSize, func(batch []tree.Entry) error {
		all = append(all, batch...)
		return nil
	})
	return all, err
}

func (m *pagedMockRemote) FetchTreePaged(ctx context.Context, treeHash []byte, pageSize int32, onPage func([]tree.Entry) error) error {
	m.callCount.Add(1)
	if pageSize <= 0 {
		pageSize = m.pageSize
	}

	offset := 0
	for offset < m.totalEntries {
		end := offset + int(pageSize)
		if end > m.totalEntries {
			end = m.totalEntries
		}

		batch := make([]tree.Entry, end-offset)
		for i := offset; i < end; i++ {
			batch[i-offset] = tree.Entry{
				Name: "generated_file_" + strconv.Itoa(i) + ".dat",
				Size: 1024,
				Mode: 0100644,
			}
		}

		if err := onPage(batch); err != nil {
			return err
		}
		offset = end
	}
	return nil
}

func (m *pagedMockRemote) StreamBlob(ctx context.Context, sha256Bytes []byte, dst io.Writer) error {
	return nil
}

func (m *pagedMockRemote) Close() error {
	return nil
}

func TestScale_PagedDirectoryStreaming(t *testing.T) {
	const totalFiles = 12500
	const pageSize = 5000

	mockRemote := &pagedMockRemote{
		totalEntries: totalFiles,
		pageSize:     pageSize,
	}

	metaCache := metadata.NewMetadataCacheWithLimit(50000)
	metaCache.SetRootTreeHash("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	fsInstance := fs.NewFS(t.TempDir(), t.TempDir(), metaCache, fs.WithRemoteClient(mockRemote))

	// Ensure directory loaded using paged streaming
	ctx := context.Background()
	err := fsInstance.EnsureDirectoryLoaded(ctx, "")
	if err != nil {
		t.Fatalf("EnsureDirectoryLoaded failed: %v", err)
	}

	// 12,500 entries should have been populated
	if metaCache.Count() != totalFiles {
		t.Errorf("expected %d cached entries, got %d", totalFiles, metaCache.Count())
	}

	// Verify entries were loaded correctly
	for i := 0; i < totalFiles; i += 1000 {
		name := "generated_file_" + strconv.Itoa(i) + ".dat"
		meta, ok := metaCache.Get(name)
		if !ok || meta.ByteSize != 1024 {
			t.Errorf("missing or invalid metadata for %s", name)
		}
	}

	// Verify O(k) ListDir returns all 12,500 children
	children := metaCache.ListDir("")
	if len(children) != totalFiles {
		t.Errorf("expected ListDir to return %d children, got %d", totalFiles, len(children))
	}
}

func TestScale_BoundedLRUMemoryCeiling(t *testing.T) {
	// Set an aggressive memory cap of 500 entries
	const cacheLimit = 500
	const filesTraversed = 3000

	metaCache := metadata.NewMetadataCacheWithLimit(cacheLimit)

	for i := 0; i < filesTraversed; i++ {
		name := fmt.Sprintf("service/pkg/file_%05d.go", i)
		metaCache.Set(name, metadata.Metadata{
			Name:     fmt.Sprintf("file_%05d.go", i),
			ByteSize: int64(i * 10),
			Mode:     0100644,
		})

		// Under continuous writing/traversal, count should NEVER exceed cacheLimit
		if metaCache.Count() > cacheLimit {
			t.Fatalf("cache exceeded bounded limit: count=%d, limit=%d", metaCache.Count(), cacheLimit)
		}
	}

	if metaCache.Count() != cacheLimit {
		t.Errorf("expected final count %d, got %d", cacheLimit, metaCache.Count())
	}

	// Oldest entries (e.g. file_00000.go) should have been evicted
	if _, ok := metaCache.Get("service/pkg/file_00000.go"); ok {
		t.Errorf("oldest entry should have been evicted by LRU")
	}

	// Recent entries (e.g. file_02999.go) must be present
	if _, ok := metaCache.Get("service/pkg/file_02999.go"); !ok {
		t.Errorf("most recent entry should be in cache")
	}
}

func TestScale_NegativeLookupSuppression(t *testing.T) {
	mockRemote := &pagedMockRemote{
		totalEntries: 0,
		pageSize:     5000,
	}

	metaCache := metadata.NewMetadataCache()
	metaCache.SetRootTreeHash("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

	fsInstance := fs.NewFS(t.TempDir(), t.TempDir(), metaCache, fs.WithRemoteClient(mockRemote))
	rootDir, err := fsInstance.Root()
	if err != nil {
		t.Fatalf("failed to get root dir: %v", err)
	}

	dir := rootDir.(*fs.Dir)
	ctx := context.Background()

	// First Lookup for missing file: should return ENOENT and cache negative hit
	_, err = dir.Lookup(ctx, "nonexistent.h")
	if err == nil {
		t.Fatalf("expected ENOENT for nonexistent.h")
	}

	if !metaCache.IsNegative("nonexistent.h") {
		t.Errorf("expected negative lookup cache for nonexistent.h")
	}

	// Subsequent lookups should hit the negative cache instantly without querying
	for i := 0; i < 100; i++ {
		_, err = dir.Lookup(ctx, "nonexistent.h")
		if err == nil {
			t.Fatalf("expected ENOENT from negative cache")
		}
	}
}
