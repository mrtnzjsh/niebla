package cas

import (
	"context"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GCStats records metrics from a CAS garbage collection execution.
type GCStats struct {
	TotalScannedBlobs int    `json:"total_scanned_blobs"`
	LiveBlobs         int    `json:"live_blobs"`
	UnreferencedBlobs int    `json:"unreferenced_blobs"`
	DeletedBlobs      int    `json:"deleted_blobs"`
	BytesReclaimed    uint64 `json:"bytes_reclaimed"`
	SkippedGraceBlobs int    `json:"skipped_grace_blobs"`
}

// GCOptions configures the garbage collection execution.
type GCOptions struct {
	GracePeriod time.Duration // Grace period for recently modified/created blobs (default: 24h)
	DryRun      bool          // If true, reports what would be deleted without removing files
}

// CollectGarbage walks the physical storage directory of the CAS engine, compares existing files against
// the liveSet of known SHA-256 hex hashes, and purges unreferenced blobs older than the grace period.
func (s *Storage) CollectGarbage(
	ctx context.Context,
	liveSet map[string]bool,
	opts GCOptions,
	onDeleteBlob func(sha256Hex string) error,
) (*GCStats, error) {
	if s == nil || s.baseDir == "" {
		return nil, fmt.Errorf("cannot run GC on nil or uninitialized CAS storage")
	}

	gracePeriod := opts.GracePeriod
	if gracePeriod <= 0 {
		gracePeriod = 24 * time.Hour
	}

	now := time.Now()
	stats := &GCStats{}

	emptyDirs := make(map[string]bool)

	err := filepath.WalkDir(s.baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		name := d.Name()
		if d.IsDir() {
			if name == tmpSubDir || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// A CAS blob file name is a 64-character lowercase hex string
		if len(name) != 64 {
			return nil
		}
		if _, hexErr := hex.DecodeString(name); hexErr != nil {
			return nil
		}

		stats.TotalScannedBlobs++
		shaHex := strings.ToLower(name)

		if liveSet[shaHex] {
			stats.LiveBlobs++
			return nil
		}

		info, sErr := d.Info()
		if sErr != nil {
			return nil
		}

		// Protect recent blobs within the grace period (e.g. in-flight uploads)
		if now.Sub(info.ModTime()) < gracePeriod {
			stats.SkippedGraceBlobs++
			return nil
		}

		stats.UnreferencedBlobs++
		stats.BytesReclaimed += uint64(info.Size())

		if !opts.DryRun {
			if rmErr := os.Remove(path); rmErr == nil {
				stats.DeletedBlobs++
				parent := filepath.Dir(path)
				emptyDirs[parent] = true
				if onDeleteBlob != nil {
					_ = onDeleteBlob(shaHex)
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed during CAS garbage collection walk: %w", err)
	}

	// Clean up empty shard subdirectories
	if !opts.DryRun {
		for dir := range emptyDirs {
			_ = os.Remove(dir)
			_ = os.Remove(filepath.Dir(dir))
		}
	}

	return stats, nil
}

