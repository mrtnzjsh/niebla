package materialize

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"niebla/pkg/cas"
	"niebla/pkg/ignore"
	"niebla/pkg/tree"
)

// DefaultConcurrency is the default worker pool size for concurrent blob materialization.
const DefaultConcurrency = 8

// BlobFetcher streams the raw bytes of a blob identified by sha256 into w.
type BlobFetcher func(ctx context.Context, sha256 []byte, w io.Writer) error

// Options configures how MaterializeTree populates the destination directory.
type Options struct {
	// Concurrency controls how many file blobs are materialized in parallel.
	// Defaults to DefaultConcurrency (8) if <= 0.
	Concurrency int

	// HardlinkCAS, if non-nil, enables zero-copy hardlinking (os.Link) directly from
	// a local CAS storage directory whenever a blob is already present on disk.
	// Useful for ephemeral read-only CI checkouts on the same filesystem.
	HardlinkCAS *cas.Storage

	// Filter optionally excludes matching paths from materialization.
	Filter ignore.Matcher
}

// Stats reports metrics about a completed MaterializeTree invocation.
type Stats struct {
	FilesMaterialized  int    `json:"files_materialized"`
	DirectoriesCreated int    `json:"directories_created"`
	BytesWritten       uint64 `json:"bytes_written"`
	HardlinkedFiles    int    `json:"hardlinked_files"`
}

type fileJob struct {
	relPath  string
	destPath string
	entry    tree.Entry
}

func collectMaterializationJobs(
	ctx context.Context,
	rootTreeHash []byte,
	destDir string,
	fetchTree tree.FetchTreeFunc,
	filter ignore.Matcher,
	stats *Stats,
) ([]fileJob, error) {
	var jobs []fileJob
	var walk func(treeHash []byte, relPrefix string) error
	walk = func(treeHash []byte, relPrefix string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(treeHash) == 0 || bytes.Equal(treeHash, tree.EmptyTreeHash) {
			return nil
		}

		entries, err := fetchTree(treeHash)
		if err != nil {
			return fmt.Errorf("failed to fetch tree %x: %w", treeHash, err)
		}

		for _, entry := range entries {
			relPath := entry.Name
			if relPrefix != "" {
				relPath = relPrefix + "/" + entry.Name
			}

			if filter != nil && filter.Match(relPath, entry.IsDir()) {
				continue
			}

			destPath := filepath.Join(destDir, filepath.FromSlash(relPath))
			if entry.IsDir() {
				if err := os.MkdirAll(destPath, 0755); err != nil {
					return fmt.Errorf("failed to create directory %s: %w", destPath, err)
				}
				stats.DirectoriesCreated++
				if err := walk(entry.SubTreeHash, relPath); err != nil {
					return err
				}
			} else {
				jobs = append(jobs, fileJob{
					relPath:  relPath,
					destPath: destPath,
					entry:    entry,
				})
			}
		}
		return nil
	}

	if err := walk(rootTreeHash, ""); err != nil {
		return nil, err
	}
	return jobs, nil
}

func executeMaterializationJobs(
	ctx context.Context,
	jobs []fileJob,
	concurrency int,
	fetchBlob BlobFetcher,
	hardlinkCAS *cas.Storage,
	stats *Stats,
) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobCh := make(chan fileJob, len(jobs))
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)

	var (
		filesCount    atomic.Int64
		hardlinkCount atomic.Int64
		bytesWritten  atomic.Uint64
		firstErr      error
		errOnce       sync.Once
		wg            sync.WaitGroup
	)

	if concurrency > len(jobs) {
		concurrency = len(jobs)
	}

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				if ctx.Err() != nil {
					return
				}
				nBytes, hardlinked, err := materializeFile(ctx, job, fetchBlob, hardlinkCAS)
				if err != nil {
					errOnce.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
				filesCount.Add(1)
				bytesWritten.Add(nBytes)
				if hardlinked {
					hardlinkCount.Add(1)
				}
			}
		}()
	}

	wg.Wait()
	if firstErr != nil {
		return firstErr
	}

	stats.FilesMaterialized = int(filesCount.Load())
	stats.HardlinkedFiles = int(hardlinkCount.Load())
	stats.BytesWritten = bytesWritten.Load()
	return nil
}

// MaterializeTree walks the Merkle tree rooted at rootTreeHash and populates destDir.
func MaterializeTree(
	ctx context.Context,
	rootTreeHash []byte,
	destDir string,
	fetchTree tree.FetchTreeFunc,
	fetchBlob BlobFetcher,
	opts Options,
) (*Stats, error) {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create destination directory %s: %w", destDir, err)
	}

	stats := &Stats{}
	if len(rootTreeHash) == 0 || bytes.Equal(rootTreeHash, tree.EmptyTreeHash) {
		return stats, nil
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}

	jobs, err := collectMaterializationJobs(ctx, rootTreeHash, destDir, fetchTree, opts.Filter, stats)
	if err != nil {
		return nil, err
	}

	if len(jobs) == 0 {
		return stats, nil
	}

	if err := executeMaterializationJobs(ctx, jobs, concurrency, fetchBlob, opts.HardlinkCAS, stats); err != nil {
		return nil, err
	}

	return stats, nil
}

func materializeFile(
	ctx context.Context,
	job fileJob,
	fetchBlob BlobFetcher,
	hardlinkCAS *cas.Storage,
) (uint64, bool, error) {
	if err := os.MkdirAll(filepath.Dir(job.destPath), 0755); err != nil {
		return 0, false, fmt.Errorf("failed to create parent dir for %s: %w", job.relPath, err)
	}

	perm := os.FileMode(0644)
	if job.entry.Mode&0111 != 0 {
		perm = 0755
	}

	// Fast-path: try hardlinking from local CAS if configured and blob exists
	if hardlinkCAS != nil && len(job.entry.BlobSHA256) > 0 && hardlinkCAS.Exists(job.entry.BlobSHA256) {
		shaHex := hex.EncodeToString(job.entry.BlobSHA256)
		if casPath, err := hardlinkCAS.BlobPath(shaHex); err == nil {
			_ = os.Remove(job.destPath)
			if linkErr := os.Link(casPath, job.destPath); linkErr == nil {
				return job.entry.Size, true, nil
			}
		}
	}

	if fetchBlob == nil {
		return 0, false, fmt.Errorf("no BlobFetcher provided to materialize %s", job.relPath)
	}

	_ = os.Remove(job.destPath)
	f, err := os.OpenFile(job.destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, false, fmt.Errorf("failed to create file %s: %w", job.destPath, err)
	}

	cw := &countingWriter{w: f}
	fetchErr := fetchBlob(ctx, job.entry.BlobSHA256, cw)
	closeErr := f.Close()
	if fetchErr != nil {
		_ = os.Remove(job.destPath)
		return 0, false, fmt.Errorf("failed to fetch blob for %s: %w", job.relPath, fetchErr)
	}
	if closeErr != nil {
		return 0, false, fmt.Errorf("failed to close file %s: %w", job.destPath, closeErr)
	}

	if err := os.Chmod(job.destPath, perm); err != nil {
		return 0, false, fmt.Errorf("failed to chmod %s: %w", job.destPath, err)
	}

	return cw.n, false, nil
}

type countingWriter struct {
	w io.Writer
	n uint64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += uint64(n)
	return n, err
}
