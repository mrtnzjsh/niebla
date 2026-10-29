package grpc

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"niebla/internal/db"
	"niebla/pkg/cas"
	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func putBlobAndTree(t *testing.T, ctx context.Context, database *db.DB, casStorage *cas.Storage, entries map[string]string) []byte {
	t.Helper()
	var treeEntries []tree.Entry
	for name, content := range entries {
		data := []byte(content)
		hashBytes, _, size, storagePath, err := casStorage.Put(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("casStorage.Put(%s) failed: %v", name, err)
		}
		if err := database.InsertBlob(ctx, hashBytes, size, storagePath); err != nil {
			t.Fatalf("database.InsertBlob(%s) failed: %v", name, err)
		}
		treeEntries = append(treeEntries, tree.Entry{
			Name:       name,
			Mode:       0100644,
			Size:       uint64(size),
			BlobSHA256: hashBytes,
		})
	}
	rootHash := tree.ComputeTreeHash(treeEntries)
	if err := database.InsertTreeNodes(ctx, rootHash, treeEntries); err != nil {
		t.Fatalf("database.InsertTreeNodes failed: %v", err)
	}
	return rootHash
}

func TestSubmitCL_OutOfOrderAndThreeWayMerge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	ctx := context.Background()
	client, database, casStorage, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Both CL 2 and CL 3 branch from initial root CL 1 (EmptyTreeHash)
	treeCL2 := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"frontend.ts": "export const ui = true;\n",
	})
	treeCL3 := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"backend.go": "package main\n",
	})

	respCL2, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "alice",
		Description:  "feat: add frontend.ts",
		RootTreeHash: treeCL2,
		ParentCl:     1,
	})
	if err != nil {
		t.Fatalf("ShelveCL(CL2) failed: %v", err)
	}

	respCL3, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "bob",
		Description:  "feat: add backend.go",
		RootTreeHash: treeCL3,
		ParentCl:     1,
	})
	if err != nil {
		t.Fatalf("ShelveCL(CL3) failed: %v", err)
	}

	// Submit CL 3 FIRST (higher CL ID submitted before lower CL ID)
	if _, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: respCL3.ClId}); err != nil {
		t.Fatalf("SubmitCL(CL3) failed: %v", err)
	}

	// Submit CL 2 SECOND -> must 3-way merge onto CL 3 and become new HeadCL
	if _, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: respCL2.ClId}); err != nil {
		t.Fatalf("SubmitCL(CL2) failed: %v", err)
	}

	headResp, err := client.GetHeadCL(ctx, &nieblav1.GetHeadCLRequest{})
	if err != nil {
		t.Fatalf("GetHeadCL failed: %v", err)
	}
	if headResp.ClId != respCL2.ClId {
		t.Errorf("expected HeadCL to be CL %d (most recently submitted), got CL %d", respCL2.ClId, headResp.ClId)
	}

	// Verify merged root tree contains BOTH frontend.ts (from CL 2) and backend.go (from CL 3)
	entries, err := database.GetTreeEntries(ctx, headResp.RootTreeHash)
	if err != nil {
		t.Fatalf("GetTreeEntries on merged head failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 files in merged head tree, got %d: %+v", len(entries), entries)
	}
	if entries[0].Name != "backend.go" || entries[1].Name != "frontend.ts" {
		t.Errorf("unexpected merged entries: %+v", entries)
	}
}

func TestSubmitCL_ConcurrentDisjointRace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	ctx := context.Background()
	client, database, casStorage, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	const numWorkers = 10
	clIDs := make([]int64, numWorkers)

	// Pre-shelve 10 disjoint CLs all branched from root CL 1
	for i := 0; i < numWorkers; i++ {
		fileName := fmt.Sprintf("worker_%02d.txt", i)
		rootHash := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
			fileName: fmt.Sprintf("payload from worker %d\n", i),
		})
		resp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
			Author:       fmt.Sprintf("agent-%d", i),
			Description:  fmt.Sprintf("concurrent disjoint change %d", i),
			RootTreeHash: rootHash,
			ParentCl:     1,
		})
		if err != nil {
			t.Fatalf("ShelveCL(%d) failed: %v", i, err)
		}
		clIDs[i] = resp.ClId
	}

	// Fire all 10 SubmitCL calls simultaneously
	startGate := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(clID int64) {
			defer wg.Done()
			<-startGate
			_, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID})
			if err != nil {
				errCh <- fmt.Errorf("SubmitCL(%d) failed: %w", clID, err)
			}
		}(clIDs[i])
	}

	close(startGate)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent submit error: %v", err)
	}

	// Verify final trunk head contains all 10 files with zero clobbering
	headResp, err := client.GetHeadCL(ctx, &nieblav1.GetHeadCLRequest{})
	if err != nil {
		t.Fatalf("GetHeadCL failed: %v", err)
	}

	entries, err := database.GetTreeEntries(ctx, headResp.RootTreeHash)
	if err != nil {
		t.Fatalf("GetTreeEntries on final head failed: %v", err)
	}
	if len(entries) != numWorkers {
		t.Fatalf("expected all %d disjoint files in final head tree, got %d: %+v", numWorkers, len(entries), entries)
	}
	for i := 0; i < numWorkers; i++ {
		expectedName := fmt.Sprintf("worker_%02d.txt", i)
		if entries[i].Name != expectedName {
			t.Errorf("expected entry[%d] = %q, got %q", i, expectedName, entries[i].Name)
		}
	}
}

func TestSubmitCL_ConcurrentConflictingEdits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	ctx := context.Background()
	client, database, casStorage, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Both CL A and CL B branch from CL 1 and modify the exact same line in shared.txt
	treeA := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"shared.txt": "line 1: version A\n",
	})
	treeB := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"shared.txt": "line 1: version B\n",
	})

	respA, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "agent-a",
		Description:  "edit shared.txt version A",
		RootTreeHash: treeA,
		ParentCl:     1,
	})
	if err != nil {
		t.Fatalf("ShelveCL(A) failed: %v", err)
	}

	respB, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "agent-b",
		Description:  "edit shared.txt version B",
		RootTreeHash: treeB,
		ParentCl:     1,
	})
	if err != nil {
		t.Fatalf("ShelveCL(B) failed: %v", err)
	}

	var successes int32
	var conflicts int32
	var failedCLID int64

	startGate := make(chan struct{})
	var wg sync.WaitGroup

	for _, clID := range []int64{respA.ClId, respB.ClId} {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			<-startGate
			_, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: id})
			if err == nil {
				atomic.AddInt32(&successes, 1)
			} else if status.Code(err) == codes.FailedPrecondition {
				atomic.AddInt32(&conflicts, 1)
				atomic.StoreInt64(&failedCLID, id)
			} else {
				t.Errorf("unexpected error for CL %d: %v", id, err)
			}
		}(clID)
	}

	close(startGate)
	wg.Wait()

	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected exactly 1 success and 1 conflict, got successes=%d conflicts=%d", successes, conflicts)
	}

	// Verify the conflicting CL was marked with needs_rebase = true
	conflictedCL, err := database.GetCL(ctx, failedCLID)
	if err != nil {
		t.Fatalf("GetCL(%d) failed: %v", failedCLID, err)
	}
	if !conflictedCL.NeedsRebase {
		t.Errorf("expected conflicted CL %d to have NeedsRebase = true", failedCLID)
	}
	if conflictedCL.Status != "shelved" {
		t.Errorf("expected conflicted CL %d to remain 'shelved', got %q", failedCLID, conflictedCL.Status)
	}
}
