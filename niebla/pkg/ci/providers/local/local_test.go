package local

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"niebla/pkg/cas"
	"niebla/pkg/ci"
	"niebla/pkg/tree"
)

type recordingReporter struct {
	mu      sync.Mutex
	reports []ci.CheckRunReport
}

func (r *recordingReporter) ReportStatus(ctx context.Context, report ci.CheckRunReport) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, report)
	return nil
}

func (r *recordingReporter) getReports() []ci.CheckRunReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ci.CheckRunReport, len(r.reports))
	copy(out, r.reports)
	return out
}

func setupTestCASAndTree(t *testing.T) (*cas.Storage, string, tree.FetchTreeFunc, func(ctx context.Context, sha256 []byte, w io.Writer) error) {
	tempDir := t.TempDir()
	casStorage, err := cas.NewStorage(filepath.Join(tempDir, "cas"))
	if err != nil {
		t.Fatalf("NewStorage: %v", err)
	}

	// Write a test file into CAS
	fileContent := []byte("echo 'Niebla Local CI Runner works!'")
	fHash, _, _, _, err := casStorage.Put(bytes.NewReader(fileContent))
	if err != nil {
		t.Fatalf("casStorage.Put: %v", err)
	}

	entries := []tree.Entry{
		{
			Name:       "test.sh",
			Mode:       0100755,
			Size:       uint64(len(fileContent)),
			BlobSHA256: fHash,
		},
	}
	tree.SortEntries(entries)
	treeHash := tree.ComputeTreeHash(entries)

	treeMap := map[string][]tree.Entry{
		string(treeHash): entries,
	}

	fetchTree := func(hash []byte) ([]tree.Entry, error) {
		if e, ok := treeMap[string(hash)]; ok {
			return e, nil
		}
		return nil, fmt.Errorf("tree not found")
	}

	fetchBlob := func(ctx context.Context, hash []byte, w io.Writer) error {
		f, _, err := casStorage.OpenBlob(hash)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	}

	return casStorage, hex.EncodeToString(treeHash), fetchTree, fetchBlob
}

func TestLocalRunner_Success(t *testing.T) {
	casStorage, treeHashHex, fetchTree, fetchBlob := setupTestCASAndTree(t)
	reporter := &recordingReporter{}
	workDir := filepath.Join(t.TempDir(), "ci-work")

	provider := NewProvider(Config{
		Name:           "local-test",
		WorkDir:        workDir,
		Command:        []string{"sh", "test.sh"},
		TimeoutSeconds: 30,
		CASStorage:     casStorage,
		TreeFetcher:    fetchTree,
		BlobFetcher:    fetchBlob,
		Reporter:       reporter,
	})

	req := ci.DispatchRequest{
		CLID:        15,
		Patchset:    1,
		TreeHash:    treeHashHex,
		Author:      "tester",
		ServerURL:   "http://localhost:8080",
		RunnerToken: "token-abc",
	}

	destDir, err := provider.Trigger(context.Background(), req)
	if err != nil {
		t.Fatalf("expected trigger to succeed, got: %v", err)
	}

	// Verify materialized file exists
	testFile := filepath.Join(destDir, "test.sh")
	if _, err := os.Stat(testFile); err != nil {
		t.Fatalf("expected %s to exist on disk: %v", testFile, err)
	}

	reports := reporter.getReports()
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports (running, passed), got %d: %+v", len(reports), reports)
	}
	if reports[0].Status != "running" {
		t.Errorf("expected first report status 'running', got %s", reports[0].Status)
	}
	if reports[1].Status != "passed" {
		t.Errorf("expected second report status 'passed', got %s", reports[1].Status)
	}
	if reports[1].LogBlobSHA256 == "" {
		t.Errorf("expected non-empty log blob SHA256 in passed report")
	}

	// Verify log blob exists in CAS
	logBytes, err := hex.DecodeString(reports[1].LogBlobSHA256)
	if err != nil {
		t.Fatalf("invalid log hex: %v", err)
	}
	logFile, _, err := casStorage.OpenBlob(logBytes)
	if err != nil {
		t.Fatalf("failed to retrieve log from CAS: %v", err)
	}
	defer logFile.Close()
	logContent, _ := io.ReadAll(logFile)
	if !bytes.Contains(logContent, []byte("Niebla Local CI Runner works!")) {
		t.Errorf("unexpected log content: %s", string(logContent))
	}
}

func TestLocalRunner_Failure(t *testing.T) {
	casStorage, _, _, _ := setupTestCASAndTree(t)
	reporter := &recordingReporter{}
	workDir := filepath.Join(t.TempDir(), "ci-work")

	provider := NewProvider(Config{
		Name:           "local-fail",
		WorkDir:        workDir,
		Command:        []string{"sh", "-c", "echo 'compilation error line 42' >&2; exit 2"},
		TimeoutSeconds: 30,
		CASStorage:     casStorage,
		Reporter:       reporter,
	})

	req := ci.DispatchRequest{
		CLID:     20,
		Patchset: 2,
		Author:   "dev",
	}

	_, err := provider.Trigger(context.Background(), req)
	if err == nil {
		t.Fatalf("expected trigger to return error for failing command")
	}

	reports := reporter.getReports()
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports (running, failed), got %d", len(reports))
	}
	if reports[1].Status != "failed" {
		t.Errorf("expected final status 'failed', got %s", reports[1].Status)
	}

	// Verify failure log was captured in CAS
	logBytes, _ := hex.DecodeString(reports[1].LogBlobSHA256)
	logFile, _, err := casStorage.OpenBlob(logBytes)
	if err != nil {
		t.Fatalf("failed to get failure log: %v", err)
	}
	defer logFile.Close()
	logContent, _ := io.ReadAll(logFile)
	if !bytes.Contains(logContent, []byte("compilation error line 42")) {
		t.Errorf("expected error output in log, got: %s", string(logContent))
	}
}

func TestLocalRunner_Timeout(t *testing.T) {
	reporter := &recordingReporter{}
	workDir := filepath.Join(t.TempDir(), "ci-work")

	provider := NewProvider(Config{
		Name:           "local-timeout",
		WorkDir:        workDir,
		Command:        []string{"sleep", "5"},
		TimeoutSeconds: 1, // 1 second timeout
		Reporter:       reporter,
	})

	req := ci.DispatchRequest{
		CLID:     30,
		Patchset: 1,
	}

	_, err := provider.Trigger(context.Background(), req)
	if err == nil {
		t.Fatalf("expected trigger to fail due to timeout")
	}

	reports := reporter.getReports()
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(reports))
	}
	if reports[1].Status != "canceled" && reports[1].Status != "failed" {
		t.Errorf("expected canceled/failed status on timeout, got %s", reports[1].Status)
	}
}
