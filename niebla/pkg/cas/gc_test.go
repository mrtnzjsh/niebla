package cas

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

func TestCollectGarbage(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cas-gc-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	ctx := context.Background()

	// 1. Put live blob
	liveData := []byte("live blob content")
	liveInfo, err := storage.Put(bytes.NewReader(liveData))
	if err != nil {
		t.Fatalf("Put live blob failed: %v", err)
	}

	// 2. Put dead blob (older than grace period)
	deadData := []byte("dead old blob content")
	deadInfo, err := storage.Put(bytes.NewReader(deadData))
	if err != nil {
		t.Fatalf("Put dead blob failed: %v", err)
	}
	deadPath, _ := storage.BlobPath(deadInfo.HexHash)
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(deadPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes failed: %v", err)
	}

	// 3. Put recent dead blob (within grace period)
	recentData := []byte("recent unreferenced blob")
	recentInfo, err := storage.Put(bytes.NewReader(recentData))
	if err != nil {
		t.Fatalf("Put recent blob failed: %v", err)
	}

	liveSet := map[string]bool{
		liveInfo.HexHash: true,
	}

	// Test DryRun
	dryStats, err := storage.CollectGarbage(ctx, liveSet, GCOptions{
		GracePeriod: 24 * time.Hour,
		DryRun:      true,
	}, nil)
	if err != nil {
		t.Fatalf("CollectGarbage dry-run failed: %v", err)
	}
	if dryStats.TotalScannedBlobs != 3 {
		t.Errorf("expected 3 scanned blobs, got %d", dryStats.TotalScannedBlobs)
	}
	if dryStats.LiveBlobs != 1 {
		t.Errorf("expected 1 live blob, got %d", dryStats.LiveBlobs)
	}
	if dryStats.SkippedGraceBlobs != 1 {
		t.Errorf("expected 1 skipped grace blob, got %d", dryStats.SkippedGraceBlobs)
	}
	if dryStats.UnreferencedBlobs != 1 {
		t.Errorf("expected 1 unreferenced blob, got %d", dryStats.UnreferencedBlobs)
	}
	if dryStats.DeletedBlobs != 0 {
		t.Errorf("expected 0 deleted blobs in dry-run, got %d", dryStats.DeletedBlobs)
	}

	// Verify dead file still exists after dry-run
	if !storage.Exists(deadInfo.HashBytes) {
		t.Errorf("dead blob should still exist after dry-run")
	}

	// Test Live Run
	var deletedHashes []string
	stats, err := storage.CollectGarbage(ctx, liveSet, GCOptions{
		GracePeriod: 24 * time.Hour,
		DryRun:      false,
	}, func(shaHex string) error {
		deletedHashes = append(deletedHashes, shaHex)
		return nil
	})
	if err != nil {
		t.Fatalf("CollectGarbage live run failed: %v", err)
	}

	if stats.DeletedBlobs != 1 {
		t.Errorf("expected 1 deleted blob, got %d", stats.DeletedBlobs)
	}
	if len(deletedHashes) != 1 || deletedHashes[0] != deadInfo.HexHash {
		t.Errorf("expected deleted hash %s, got %v", deadInfo.HexHash, deletedHashes)
	}

	// Verify dead blob was actually removed from disk
	if storage.Exists(deadInfo.HashBytes) {
		t.Errorf("dead blob should have been removed from disk")
	}

	// Verify live blob and recent blob still exist
	if !storage.Exists(liveInfo.HashBytes) {
		t.Errorf("live blob should still exist")
	}
	if !storage.Exists(recentInfo.HashBytes) {
		t.Errorf("recent blob within grace period should still exist")
	}
}

