package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"niebla/pkg/cas"
	"niebla/pkg/tree"
)

type mockRemoteClient struct {
	blobs     map[string][]byte
	trees     map[string][]tree.Entry
	callCount atomic.Int64
	delay     time.Duration
}

func (m *mockRemoteClient) FetchTree(ctx context.Context, treeHash []byte) ([]tree.Entry, error) {
	m.callCount.Add(1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	key := hex.EncodeToString(treeHash)
	entries, ok := m.trees[key]
	if !ok {
		return nil, fmt.Errorf("tree %s not found", key)
	}
	return entries, nil
}

func (m *mockRemoteClient) FetchTreePaged(ctx context.Context, treeHash []byte, pageSize int32, onPage func([]tree.Entry) error) error {
	entries, err := m.FetchTree(ctx, treeHash)
	if err != nil {
		return err
	}
	return onPage(entries)
}

func (m *mockRemoteClient) StreamBlob(ctx context.Context, sha256Bytes []byte, dst io.Writer) error {
	m.callCount.Add(1)
	if m.delay > 0 {
		time.Sleep(m.delay)
	}
	key := hex.EncodeToString(sha256Bytes)
	data, ok := m.blobs[key]
	if !ok {
		return fmt.Errorf("blob %s not found", key)
	}
	_, err := dst.Write(data)
	return err
}

func (m *mockRemoteClient) Close() error {
	return nil
}

func TestBlobFetcher_EnsureBlob_FastPathExisting(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := cas.NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("failed to init cas storage: %v", err)
	}

	content := []byte("already existing blob content")
	_, hexHash, _, _, err := storage.Put(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("failed to put blob: %v", err)
	}

	mock := &mockRemoteClient{}
	fetcher := NewBlobFetcher(storage, mock)

	path, err := fetcher.EnsureBlob(context.Background(), hexHash)
	if err != nil {
		t.Fatalf("EnsureBlob failed: %v", err)
	}

	if mock.callCount.Load() != 0 {
		t.Errorf("expected 0 remote calls for existing blob, got %d", mock.callCount.Load())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read blob file: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("blob content mismatch: got %q, want %q", string(data), string(content))
	}
}

func TestBlobFetcher_EnsureBlob_OnDemandFetchAndCache(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := cas.NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("failed to init cas storage: %v", err)
	}

	content := []byte("remote streamed blob content")
	h := sha256.Sum256(content)
	hexHash := hex.EncodeToString(h[:])

	mock := &mockRemoteClient{
		blobs: map[string][]byte{
			hexHash: content,
		},
	}
	fetcher := NewBlobFetcher(storage, mock)

	// First fetch: should stream from remote and cache
	path, err := fetcher.EnsureBlob(context.Background(), hexHash)
	if err != nil {
		t.Fatalf("first EnsureBlob failed: %v", err)
	}

	if mock.callCount.Load() != 1 {
		t.Errorf("expected 1 remote call, got %d", mock.callCount.Load())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read cached blob file: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Errorf("blob content mismatch: got %q, want %q", string(data), string(content))
	}

	// Second fetch: should hit local cache, no extra remote call
	path2, err := fetcher.EnsureBlob(context.Background(), hexHash)
	if err != nil {
		t.Fatalf("second EnsureBlob failed: %v", err)
	}
	if path2 != path {
		t.Errorf("expected same path %q, got %q", path, path2)
	}
	if mock.callCount.Load() != 1 {
		t.Errorf("expected remote call count to remain 1, got %d", mock.callCount.Load())
	}
}

func TestBlobFetcher_EnsureBlob_ConcurrentCoalescing(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := cas.NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("failed to init cas storage: %v", err)
	}

	content := []byte("concurrently requested blob")
	h := sha256.Sum256(content)
	hexHash := hex.EncodeToString(h[:])

	mock := &mockRemoteClient{
		blobs: map[string][]byte{
			hexHash: content,
		},
		delay: 50 * time.Millisecond,
	}
	fetcher := NewBlobFetcher(storage, mock)

	var wg sync.WaitGroup
	const callers = 10
	errorsCh := make(chan error, callers)

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := fetcher.EnsureBlob(context.Background(), hexHash)
			if err != nil {
				errorsCh <- err
				return
			}
			d, err := os.ReadFile(p)
			if err != nil || !bytes.Equal(d, content) {
				errorsCh <- fmt.Errorf("read verification failed: %v", err)
			}
		}()
	}

	wg.Wait()
	close(errorsCh)

	for err := range errorsCh {
		t.Fatalf("concurrent caller error: %v", err)
	}

	// All 10 callers should have coalesced into exactly 1 remote stream call!
	if mock.callCount.Load() != 1 {
		t.Errorf("expected exactly 1 remote call from coalescing, got %d", mock.callCount.Load())
	}
}

func TestBlobFetcher_EnsureBlob_ChecksumMismatchDetected(t *testing.T) {
	tmpDir := t.TempDir()
	storage, err := cas.NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("failed to init cas storage: %v", err)
	}

	content := []byte("legitimate content")
	h := sha256.Sum256(content)
	hexHash := hex.EncodeToString(h[:])

	corrupted := []byte("corrupted data returned by server")
	mock := &mockRemoteClient{
		blobs: map[string][]byte{
			hexHash: corrupted,
		},
	}
	fetcher := NewBlobFetcher(storage, mock)

	_, err = fetcher.EnsureBlob(context.Background(), hexHash)
	if err == nil {
		t.Fatalf("expected error on checksum mismatch, got nil")
	}

	// Verify no corrupted blob was committed to CAS
	p, _ := storage.BlobPath(hexHash)
	if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
		t.Errorf("corrupted blob should not be left in CAS storage: %s", p)
	}
}
