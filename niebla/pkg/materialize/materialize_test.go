package materialize

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"niebla/pkg/cas"
	"niebla/pkg/ignore"
	"niebla/pkg/tree"
)

type testFixture struct {
	rootHash  []byte
	fetchTree tree.FetchTreeFunc
	fetchBlob BlobFetcher
	blobs     map[string][]byte
}

func buildTestFixture(t *testing.T) *testFixture {
	t.Helper()

	blobs := make(map[string][]byte)
	addBlob := func(content string) ([]byte, uint64) {
		sum := sha256.Sum256([]byte(content))
		blobs[hex.EncodeToString(sum[:])] = []byte(content)
		return sum[:], uint64(len(content))
	}

	readmeSHA, readmeSize := addBlob("# Project Readme\n")
	scriptSHA, scriptSize := addBlob("#!/bin/sh\necho hello\n")
	mainSHA, mainSize := addBlob("package main\n\nfunc main() {}\n")
	ignoredSHA, ignoredSize := addBlob("cached artifact")

	binEntries := []tree.Entry{
		{Name: "run.sh", Mode: 0100755, Size: scriptSize, BlobSHA256: scriptSHA},
	}
	binHash := tree.ComputeTreeHash(binEntries)

	srcEntries := []tree.Entry{
		{Name: "main.go", Mode: 0100644, Size: mainSize, BlobSHA256: mainSHA},
	}
	srcHash := tree.ComputeTreeHash(srcEntries)

	cacheEntries := []tree.Entry{
		{Name: "artifact.bin", Mode: 0100644, Size: ignoredSize, BlobSHA256: ignoredSHA},
	}
	cacheHash := tree.ComputeTreeHash(cacheEntries)

	rootEntries := []tree.Entry{
		{Name: "README.md", Mode: 0100644, Size: readmeSize, BlobSHA256: readmeSHA},
		{Name: "bin", Mode: 0040755, SubTreeHash: binHash},
		{Name: "cache", Mode: 0040755, SubTreeHash: cacheHash},
		{Name: "src", Mode: 0040755, SubTreeHash: srcHash},
	}
	tree.SortEntries(rootEntries)
	rootHash := tree.ComputeTreeHash(rootEntries)

	trees := map[string][]tree.Entry{
		hex.EncodeToString(rootHash):  rootEntries,
		hex.EncodeToString(binHash):   binEntries,
		hex.EncodeToString(srcHash):   srcEntries,
		hex.EncodeToString(cacheHash): cacheEntries,
	}

	return &testFixture{
		rootHash: rootHash,
		blobs:    blobs,
		fetchTree: func(hash []byte) ([]tree.Entry, error) {
			entries, ok := trees[hex.EncodeToString(hash)]
			if !ok {
				return nil, fmt.Errorf("unknown tree %x", hash)
			}
			return entries, nil
		},
		fetchBlob: func(ctx context.Context, sha []byte, w io.Writer) error {
			data, ok := blobs[hex.EncodeToString(sha)]
			if !ok {
				return fmt.Errorf("unknown blob %x", sha)
			}
			_, err := w.Write(data)
			return err
		},
	}
}

func TestMaterializeTree_WritableCopyAndPermissions(t *testing.T) {
	fx := buildTestFixture(t)
	destDir := t.TempDir()

	stats, err := MaterializeTree(context.Background(), fx.rootHash, destDir, fx.fetchTree, fx.fetchBlob, Options{
		Concurrency: 4,
	})
	if err != nil {
		t.Fatalf("MaterializeTree failed: %v", err)
	}

	if stats.FilesMaterialized != 4 {
		t.Errorf("FilesMaterialized = %d, want 4", stats.FilesMaterialized)
	}
	if stats.DirectoriesCreated != 3 {
		t.Errorf("DirectoriesCreated = %d, want 3", stats.DirectoriesCreated)
	}
	if stats.HardlinkedFiles != 0 {
		t.Errorf("HardlinkedFiles = %d, want 0", stats.HardlinkedFiles)
	}

	// Verify file contents and permissions
	readmePath := filepath.Join(destDir, "README.md")
	data, err := os.ReadFile(readmePath)
	if err != nil || string(data) != "# Project Readme\n" {
		t.Fatalf("unexpected README.md content: %q (err=%v)", string(data), err)
	}
	info, err := os.Stat(readmePath)
	if err != nil {
		t.Fatalf("stat README.md: %v", err)
	}
	if info.Mode().Perm() != 0644 {
		t.Errorf("README.md perm = %o, want 0644", info.Mode().Perm())
	}

	// Verify writable copy can be modified in place (for --no-fuse workspaces)
	if err := os.WriteFile(readmePath, []byte("# Updated\n"), 0644); err != nil {
		t.Errorf("expected materialized file to be writable: %v", err)
	}

	// Verify executable permissions on bin/run.sh
	scriptPath := filepath.Join(destDir, "bin", "run.sh")
	scriptInfo, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("stat bin/run.sh: %v", err)
	}
	if scriptInfo.Mode().Perm() != 0755 {
		t.Errorf("bin/run.sh perm = %o, want 0755", scriptInfo.Mode().Perm())
	}
}

func TestMaterializeTree_HardlinkCASFastPath(t *testing.T) {
	fx := buildTestFixture(t)
	casDir := filepath.Join(t.TempDir(), "cas")
	casStorage, err := cas.NewStorage(casDir)
	if err != nil {
		t.Fatalf("cas.NewStorage: %v", err)
	}

	// Pre-populate all blobs in CAS except one to test both hardlink and fallback
	var skippedHex string
	for shaHex, content := range fx.blobs {
		if skippedHex == "" {
			skippedHex = shaHex
			continue
		}
		bw, err := casStorage.BeginWriter()
		if err != nil {
			t.Fatalf("BeginWriter: %v", err)
		}
		if _, err := bw.Write(content); err != nil {
			t.Fatalf("bw.Write: %v", err)
		}
		if _, _, _, _, err := bw.Commit(); err != nil {
			t.Fatalf("bw.Commit: %v", err)
		}
	}

	destDir := filepath.Join(t.TempDir(), "checkout")
	stats, err := MaterializeTree(context.Background(), fx.rootHash, destDir, fx.fetchTree, fx.fetchBlob, Options{
		Concurrency: 4,
		HardlinkCAS: casStorage,
	})
	if err != nil {
		t.Fatalf("MaterializeTree with HardlinkCAS failed: %v", err)
	}

	if stats.FilesMaterialized != 4 {
		t.Errorf("FilesMaterialized = %d, want 4", stats.FilesMaterialized)
	}
	if stats.HardlinkedFiles != 3 {
		t.Errorf("HardlinkedFiles = %d, want 3", stats.HardlinkedFiles)
	}

	// Verify that a CAS-resident file shares the exact same inode as the CAS blob
	mainContent := "package main\n\nfunc main() {}\n"
	mainSHA := sha256.Sum256([]byte(mainContent))
	mainHex := hex.EncodeToString(mainSHA[:])
	if mainHex != skippedHex {
		casPath, err := casStorage.BlobPath(mainHex)
		if err != nil {
			t.Fatalf("BlobPath: %v", err)
		}
		casStat, err := os.Stat(casPath)
		if err != nil {
			t.Fatalf("Stat casPath: %v", err)
		}
		destStat, err := os.Stat(filepath.Join(destDir, "src", "main.go"))
		if err != nil {
			t.Fatalf("Stat dest main.go: %v", err)
		}

		casIno := casStat.Sys().(*syscall.Stat_t).Ino
		destIno := destStat.Sys().(*syscall.Stat_t).Ino
		if casIno != destIno {
			t.Errorf("expected hardlinked file to share inode with CAS blob: got %d vs %d", destIno, casIno)
		}
	}
}

func TestMaterializeTree_WithIgnoreFilter(t *testing.T) {
	fx := buildTestFixture(t)
	destDir := t.TempDir()

	matcher := ignore.NewMatcher("cache/")
	stats, err := MaterializeTree(context.Background(), fx.rootHash, destDir, fx.fetchTree, fx.fetchBlob, Options{
		Filter: matcher,
	})
	if err != nil {
		t.Fatalf("MaterializeTree with Filter failed: %v", err)
	}

	if stats.FilesMaterialized != 3 {
		t.Errorf("FilesMaterialized = %d, want 3 (cache/artifact.bin excluded)", stats.FilesMaterialized)
	}
	if _, err := os.Stat(filepath.Join(destDir, "cache")); !os.IsNotExist(err) {
		t.Errorf("expected cache directory to be excluded by Filter")
	}
}
