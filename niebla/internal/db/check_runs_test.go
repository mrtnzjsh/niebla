package db

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"
)

func TestCheckRunsOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	database, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Create a dummy blob to reference in log_blob_sha256
	logContent := []byte("Build logs: compiling main.go... OK")
	logHash := sha256.Sum256(logContent)
	if err := database.InsertBlob(ctx, logHash[:], int64(len(logContent)), "ab/cd/log.txt"); err != nil {
		t.Fatalf("failed to insert test blob: %v", err)
	}

	// 2. Create a test CL
	treeHash := sha256.Sum256([]byte("tree manifest"))
	clID, err := database.CreateShelvedCL(ctx, "ci_author", "Test CL for CI", treeHash[:], 1)
	if err != nil {
		t.Fatalf("failed to create test CL: %v", err)
	}

	// 3. Test GetLatestPatchsetNumber initially (default 1)
	latestPS, err := database.GetLatestPatchsetNumber(ctx, clID)
	if err != nil {
		t.Fatalf("failed to get latest patchset number: %v", err)
	}
	if latestPS != 1 {
		t.Errorf("expected initial patchset to be 1, got %d", latestPS)
	}

	// 4. Test AreCheckRunsGreen when no check runs exist
	summary, err := database.AreCheckRunsGreen(ctx, clID, 1)
	if err != nil {
		t.Fatalf("failed to check green status: %v", err)
	}
	if summary.Green || summary.Total != 0 || summary.Pending != 0 || summary.Failed != 0 {
		t.Fatalf("expected green=false, total=0, pending=0, failed=0 for empty runs; got %+v", summary)
	}

	// 5. Upsert check run 1: queued
	run1, err := database.UpsertCheckRun(ctx, CheckRun{
		CLID:           clID,
		PatchsetNumber: 1,
		RunnerName:     "local-sandbox",
		Provider:       "local",
		Status:         "queued",
	})
	if err != nil {
		t.Fatalf("failed to upsert check run: %v", err)
	}
	if run1.ID <= 0 || run1.Status != "queued" {
		t.Errorf("unexpected run1 output: %+v", run1)
	}

	// Check AreCheckRunsGreen: pending=1, green=false
	summary, err = database.AreCheckRunsGreen(ctx, clID, 1)
	if err != nil {
		t.Fatalf("failed to check green status: %v", err)
	}
	if summary.Green || summary.Total != 1 || summary.Pending != 1 || summary.Failed != 0 {
		t.Fatalf("expected green=false, total=1, pending=1, failed=0; got %+v", summary)
	}

	// 6. Transition run 1: running
	now := time.Now().UTC()
	run1, err = database.UpsertCheckRun(ctx, CheckRun{
		CLID:           clID,
		PatchsetNumber: 1,
		RunnerName:     "local-sandbox",
		Provider:       "local",
		Status:         "running",
		StartedAt:      &now,
		ExternalURL:    "https://ci.example.com/build/1",
	})
	if err != nil {
		t.Fatalf("failed to update check run to running: %v", err)
	}
	if run1.Status != "running" || run1.ExternalURL != "https://ci.example.com/build/1" {
		t.Errorf("unexpected updated run1: %+v", run1)
	}

	// 7. Transition run 1: passed with log blob
	run1, err = database.UpsertCheckRun(ctx, CheckRun{
		CLID:           clID,
		PatchsetNumber: 1,
		RunnerName:     "local-sandbox",
		Provider:       "local",
		Status:         "passed",
		LogBlobSHA256:  logHash[:],
	})
	if err != nil {
		t.Fatalf("failed to update check run to passed: %v", err)
	}
	if run1.Status != "passed" || run1.CompletedAt == nil {
		t.Errorf("expected status 'passed' with auto-completed_at, got: %+v", run1)
	}

	// Check AreCheckRunsGreen: total=1, pending=0, failed=0 -> green=true!
	summary, err = database.AreCheckRunsGreen(ctx, clID, 1)
	if err != nil {
		t.Fatalf("failed to check green status: %v", err)
	}
	if !summary.Green || summary.Total != 1 || summary.Pending != 0 || summary.Failed != 0 {
		t.Fatalf("expected green=true, total=1, pending=0, failed=0; got %+v", summary)
	}

	// 8. Add a second runner that fails
	_, err = database.UpsertCheckRun(ctx, CheckRun{
		CLID:           clID,
		PatchsetNumber: 1,
		RunnerName:     "forgejo-lint",
		Provider:       "forgejo",
		Status:         "failed",
	})
	if err != nil {
		t.Fatalf("failed to upsert failed check run: %v", err)
	}

	summary, err = database.AreCheckRunsGreen(ctx, clID, 1)
	if err != nil {
		t.Fatalf("failed to check green status: %v", err)
	}
	if summary.Green || summary.Total != 2 || summary.Pending != 0 || summary.Failed != 1 {
		t.Fatalf("expected green=false, total=2, pending=0, failed=1; got %+v", summary)
	}

	// 9. Re-run forgejo-lint and it passes
	_, err = database.UpsertCheckRun(ctx, CheckRun{
		CLID:           clID,
		PatchsetNumber: 1,
		RunnerName:     "forgejo-lint",
		Provider:       "forgejo",
		Status:         "passed",
	})
	if err != nil {
		t.Fatalf("failed to update forgejo-lint to passed: %v", err)
	}

	summary, err = database.AreCheckRunsGreen(ctx, clID, 1)
	if err != nil {
		t.Fatalf("failed to check green status: %v", err)
	}
	if !summary.Green || summary.Total != 2 || summary.Pending != 0 || summary.Failed != 0 {
		t.Fatalf("expected green=true, total=2, pending=0, failed=0; got %+v", summary)
	}

	// 10. List check runs
	runs, err := database.ListCheckRunsForPatchset(ctx, clID, 1)
	if err != nil {
		t.Fatalf("failed to list check runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 check runs, got %d", len(runs))
	}
	// Verify alphabetical order: forgejo-lint before local-sandbox
	if runs[0].RunnerName != "forgejo-lint" || runs[1].RunnerName != "local-sandbox" {
		t.Errorf("unexpected runner order: %s, %s", runs[0].RunnerName, runs[1].RunnerName)
	}
}
