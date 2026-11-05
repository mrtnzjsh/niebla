//go:build rc

package rc_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"niebla/internal/db"
	"niebla/pkg/types"
)

func TestCIPipelineAndCASLogGating(t *testing.T) {
	cluster := SetupRCCluster(t)
	defer cluster.Cleanup()

	ctx := context.Background()

	// 1. Create a CL
	emptyTreeHash := []byte{0x10, 0x20, 0x30, 0x40}
	clID, err := cluster.DB.CreateShelvedCL(ctx, "developer", "feat: pipeline verification", emptyTreeHash, 0)
	if err != nil {
		t.Fatalf("failed to create CL: %v", err)
	}

	const patchset = 1
	runnerName := "forgejo-runner-linux"

	// 2. Insert check run in 'queued' state
	queuedRun := db.CheckRun{
		CLID:           clID,
		PatchsetNumber: patchset,
		RunnerName:     runnerName,
		Provider:       "forgejo",
		Status:         string(types.CheckRunStatusQueued),
		CreatedAt:      time.Now().UTC(),
	}
	if _, err := cluster.DB.UpsertCheckRun(ctx, queuedRun); err != nil {
		t.Fatalf("failed to insert queued check run: %v", err)
	}

	// 3. Verify CheckRunsSummary reports NOT green
	summary, err := cluster.DB.AreCheckRunsGreen(ctx, clID, patchset)
	if err != nil {
		t.Fatalf("AreCheckRunsGreen failed: %v", err)
	}
	if summary.Green {
		t.Errorf("expected summary.Green to be false when check run is queued, got true")
	}
	if summary.Pending != 1 {
		t.Errorf("expected 1 pending check run, got %d", summary.Pending)
	}

	// 4. Simulate runner execution: transition to 'running'
	runningRun := queuedRun
	runningRun.Status = string(types.CheckRunStatusRunning)
	if _, err := cluster.DB.UpsertCheckRun(ctx, runningRun); err != nil {
		t.Fatalf("failed to transition to running: %v", err)
	}

	// 5. Runner finishes, stores build log in CAS
	logOutput := []byte("=== RUN TestAll\n--- PASS: TestAll (0.05s)\nPASS\ncoverage: 88.5% of statements\n")
	blobInfo, err := cluster.CASStorage.Put(bytes.NewReader(logOutput))
	if err != nil {
		t.Fatalf("failed to write build log into CAS: %v", err)
	}
	if err := cluster.DB.InsertBlob(ctx, blobInfo.HashBytes, blobInfo.Size, blobInfo.StoragePath); err != nil {
		t.Fatalf("failed to record log blob in DB: %v", err)
	}

	// 6. Transition check run to 'passed' with CAS log hash
	passedRun := runningRun
	passedRun.Status = string(types.CheckRunStatusPassed)
	passedRun.LogBlobSHA256 = blobInfo.HashBytes
	if _, err := cluster.DB.UpsertCheckRun(ctx, passedRun); err != nil {
		t.Fatalf("failed to transition to passed: %v", err)
	}

	// 7. Verify CheckRunsSummary is now Green
	summary, err = cluster.DB.AreCheckRunsGreen(ctx, clID, patchset)
	if err != nil {
		t.Fatalf("AreCheckRunsGreen failed after pass: %v", err)
	}
	if !summary.Green {
		t.Errorf("expected summary.Green to be true, got false")
	}
	if summary.Failed != 0 || summary.Pending != 0 {
		t.Errorf("expected 0 failed and 0 pending, got failed=%d pending=%d", summary.Failed, summary.Pending)
	}

	// 8. Test Cotejo API endpoints
	client := &http.Client{}
	baseURL := cluster.CotejoServer.URL

	// A. CI Status endpoint
	resp, err := client.Get(fmt.Sprintf("%s/api/v1/ci/status?cl_id=%d&patchset=1", baseURL, clID))
	if err != nil {
		t.Fatalf("GET /api/v1/ci/status failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("ci-status returned status %d: %s", resp.StatusCode, string(body))
	}

	// B. Raw Log Download endpoint (/cl/{id}/raw?hash=...)
	resp, err = client.Get(fmt.Sprintf("%s/cl/%d/raw?hash=%s", baseURL, clID, blobInfo.HexHash))
	if err != nil {
		t.Fatalf("GET raw log failed: %v", err)
	}
	defer resp.Body.Close()
	rawDownloadedLog, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("raw blob download returned %d: %s", resp.StatusCode, string(rawDownloadedLog))
	}
	if !bytes.Equal(rawDownloadedLog, logOutput) {
		t.Errorf("downloaded log content mismatch: got %q, want %q", string(rawDownloadedLog), string(logOutput))
	}
}
