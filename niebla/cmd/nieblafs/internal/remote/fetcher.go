package remote

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"sync"

	"niebla/pkg/cas"
)

type inflightFetch struct {
	done chan struct{}
	err  error
	path string
}

// BlobFetcher manages local CAS blob caching with in-flight deduplication.
type BlobFetcher struct {
	casStorage   *cas.Storage
	remoteClient Client
	mu           sync.Mutex
	inflight     map[string]*inflightFetch
}

// NewBlobFetcher constructs an initialized BlobFetcher.
func NewBlobFetcher(casStorage *cas.Storage, remoteClient Client) *BlobFetcher {
	return &BlobFetcher{
		casStorage:   casStorage,
		remoteClient: remoteClient,
		inflight:     make(map[string]*inflightFetch),
	}
}

// EnsureBlob checks the local CAS cache for sha256Hex and streams it from remoteClient if missing.
// Multiple concurrent callers for the same blob hash coalesce onto a single gRPC stream.
func (f *BlobFetcher) EnsureBlob(ctx context.Context, sha256Hex string) (string, error) {
	if sha256Hex == "" {
		return "", fmt.Errorf("sha256 hex string cannot be empty")
	}

	shaBytes, err := hex.DecodeString(sha256Hex)
	if err != nil || len(shaBytes) != 32 {
		return "", fmt.Errorf("invalid sha256 hex string %q: %w", sha256Hex, err)
	}

	if f.casStorage != nil && f.casStorage.Exists(shaBytes) {
		return f.casStorage.BlobPath(sha256Hex)
	}

	if f.remoteClient == nil {
		return "", fmt.Errorf("remote client is nil and blob %s not in local CAS cache", sha256Hex)
	}

	f.mu.Lock()
	if existing, ok := f.inflight[sha256Hex]; ok {
		f.mu.Unlock()
		select {
		case <-existing.done:
			return existing.path, existing.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	fetch := &inflightFetch{done: make(chan struct{})}
	f.inflight[sha256Hex] = fetch
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		delete(f.inflight, sha256Hex)
		close(fetch.done)
		f.mu.Unlock()
	}()

	// Double-check local CAS in case a previous caller just committed it
	if f.casStorage != nil && f.casStorage.Exists(shaBytes) {
		fetch.path, fetch.err = f.casStorage.BlobPath(sha256Hex)
		return fetch.path, fetch.err
	}

	path, err := f.streamAndCommitBlob(ctx, shaBytes, sha256Hex)
	fetch.path = path
	fetch.err = err
	return path, err
}

func (f *BlobFetcher) streamAndCommitBlob(ctx context.Context, shaBytes []byte, sha256Hex string) (string, error) {
	if f.casStorage == nil {
		return "", fmt.Errorf("local CAS storage is not configured")
	}

	blobWriter, err := f.casStorage.BeginWriter()
	if err != nil {
		return "", fmt.Errorf("failed to begin CAS blob writer: %w", err)
	}
	defer blobWriter.Abort()

	if err := f.remoteClient.StreamBlob(ctx, shaBytes, blobWriter); err != nil {
		return "", fmt.Errorf("failed to stream blob %s from server: %w", sha256Hex, err)
	}

	blobInfo, err := blobWriter.Commit()
	if err != nil {
		return "", fmt.Errorf("failed to commit blob %s to CAS: %w", sha256Hex, err)
	}

	if !bytes.Equal(blobInfo.HashBytes, shaBytes) {
		return "", fmt.Errorf("committed blob hash %x did not match requested %x", blobInfo.HashBytes, shaBytes)
	}

	return f.casStorage.BlobPath(sha256Hex)
}

