package grpc

import (
	"context"
	"sync"
	"testing"
	"time"

	nieblav1 "niebla/pkg/proto/nieblav1"
)

func TestCascadeIntegration_DeepStackRebase(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, server, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Build a 4-tier stack: CL1 -> CL2 -> CL3 -> CL4
	// CL 1: Base changelist
	t1 := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"root.txt": "root line 1\n",
	})
	resp1, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "alice",
		Description:  "tier 1 base",
		RootTreeHash: t1,
	})
	if err != nil {
		t.Fatalf("failed to create CL 1: %v", err)
	}
	cl1 := resp1.ClId

	// CL 2: Child of CL 1
	t2 := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"root.txt":     "root line 1\n",
		"module_a.txt": "module A initial\n",
	})
	resp2, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "bob",
		Description:  "tier 2 child",
		ParentCl:     cl1,
		RootTreeHash: t2,
	})
	if err != nil {
		t.Fatalf("failed to create CL 2: %v", err)
	}
	cl2 := resp2.ClId

	// CL 3: Child of CL 2
	t3 := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"root.txt":     "root line 1\n",
		"module_a.txt": "module A initial\n",
		"module_b.txt": "module B initial\n",
	})
	resp3, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "carol",
		Description:  "tier 3 child",
		ParentCl:     cl2,
		RootTreeHash: t3,
	})
	if err != nil {
		t.Fatalf("failed to create CL 3: %v", err)
	}
	cl3 := resp3.ClId

	// CL 4: Child of CL 3
	t4 := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"root.txt":     "root line 1\n",
		"module_a.txt": "module A initial\n",
		"module_b.txt": "module B initial\n",
		"module_c.txt": "module C initial\n",
	})
	resp4, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "dave",
		Description:  "tier 4 leaf",
		ParentCl:     cl3,
		RootTreeHash: t4,
	})
	if err != nil {
		t.Fatalf("failed to create CL 4: %v", err)
	}
	cl4 := resp4.ClId

	// 2. Re-shelve CL 1 with disjoint file edit: "root.txt" gains a line
	t1Updated := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"root.txt": "root line 1\nroot line 2\n",
	})

	start := time.Now()
	_, err = client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		ClId:         cl1,
		Author:       "alice",
		Description:  "tier 1 updated",
		RootTreeHash: t1Updated,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("failed to re-shelve CL 1: %v", err)
	}

	// Sub-100ms async shelve assertion
	if elapsed > 300*time.Millisecond {
		t.Errorf("expected ShelveCL to return immediately (<300ms), took %v", elapsed)
	}

	// 3. Immediately invoke SyncWorkspace on leaf CL 4 -> triggers recursive On-Demand Sync-Through!
	syncResp, err := client.SyncWorkspace(ctx, &nieblav1.SyncWorkspaceRequest{
		ClId: cl4,
	})
	if err != nil {
		t.Fatalf("SyncWorkspace on CL 4 failed: %v", err)
	}

	if syncResp.NeedsRebase {
		t.Errorf("expected CL 4 clean rebase, got conflict: %s", syncResp.RebaseConflictDetails)
	}

	// Verify all entries exist in CL 4's synced Merkle tree
	entries, err := server.getTreeEntries(ctx, syncResp.RootTreeHash)
	if err != nil {
		t.Fatalf("failed to get CL 4 tree entries: %v", err)
	}
	nameMap := make(map[string]bool)
	for _, e := range entries {
		nameMap[e.Name] = true
	}
	for _, expected := range []string{"root.txt", "module_a.txt", "module_b.txt", "module_c.txt"} {
		if !nameMap[expected] {
			t.Errorf("expected entry %s missing in CL 4 tree: %v", expected, nameMap)
		}
	}

	// 4. Wait for background queue to idle and verify all tiers in database
	server.RebaseQueue().WaitIdle()

	for _, checkCL := range []int64{cl2, cl3, cl4} {
		record, err := database.GetCL(ctx, checkCL)
		if err != nil {
			t.Fatalf("failed to read CL %d: %v", checkCL, err)
		}
		if record.NeedsRebase {
			t.Errorf("CL %d flagged with NeedsRebase unexpectedly", checkCL)
		}
		psNum, _ := database.GetNextPatchsetNumber(ctx, checkCL)
		if psNum < 3 { // Created PS 1, auto-rebased to PS 2
			t.Errorf("expected CL %d to have patchset >= 2, nextPS = %d", checkCL, psNum)
		}
	}
}

func TestCascadeIntegration_ConflictHaltRecursion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, server, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Build a 3-tier stack: CL 1 -> CL 2 -> CL 3
	baseTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"shared.txt": "base line\n",
	})
	resp1, _ := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "alice",
		Description:  "CL 1 base",
		RootTreeHash: baseTree,
	})
	cl1 := resp1.ClId

	// CL 2 edits shared.txt
	childTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"shared.txt": "child edit\n",
	})
	resp2, _ := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "bob",
		Description:  "CL 2 child",
		ParentCl:     cl1,
		RootTreeHash: childTree,
	})
	cl2 := resp2.ClId

	// CL 3 adds grandchild.txt
	grandchildTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"shared.txt":     "child edit\n",
		"grandchild.txt": "grandchild file\n",
	})
	resp3, _ := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "carol",
		Description:  "CL 3 grandchild",
		ParentCl:     cl2,
		RootTreeHash: grandchildTree,
	})
	cl3 := resp3.ClId

	// 2. Parent CL 1 makes conflicting edit to shared.txt
	parentConflictTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"shared.txt": "parent conflicting edit\n",
	})
	_, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		ClId:         cl1,
		Author:       "alice",
		Description:  "CL 1 conflict",
		RootTreeHash: parentConflictTree,
	})
	if err != nil {
		t.Fatalf("failed to update CL 1: %v", err)
	}

	// Wait for background worker to execute rebase
	server.RebaseQueue().WaitIdle()

	// 3. Verify CL 2 is flagged with NeedsRebase and conflict summary
	cl2Record, err := database.GetCL(ctx, cl2)
	if err != nil {
		t.Fatalf("failed to get CL 2: %v", err)
	}
	if !cl2Record.NeedsRebase {
		t.Errorf("expected CL 2 NeedsRebase = true")
	}
	if cl2Record.RebaseConflictDetails == nil || *cl2Record.RebaseConflictDetails == "" {
		t.Errorf("expected conflict details on CL 2")
	}

	// 4. Verify CL 3 recursion was halted: CL 3 patchset should not have been created
	cl3NextPS, _ := database.GetNextPatchsetNumber(ctx, cl3)
	if cl3NextPS != 2 { // Only patchset 1 exists, next is 2
		t.Errorf("expected CL 3 to remain at patchset 1, got nextPS = %d", cl3NextPS)
	}
}

func TestCascadeIntegration_DisjointStacksParallel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, server, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// Stack A: A1 -> A2
	tA1 := putBlobAndTree(t, ctx, database, casStorage, map[string]string{"a.txt": "a base\n"})
	respA1, _ := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{Author: "alice", Description: "A1", RootTreeHash: tA1})
	clA1 := respA1.ClId

	tA2 := putBlobAndTree(t, ctx, database, casStorage, map[string]string{"a.txt": "a base\n", "a2.txt": "a2\n"})
	respA2, _ := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{Author: "alice2", Description: "A2", ParentCl: clA1, RootTreeHash: tA2})
	clA2 := respA2.ClId

	// Stack B: B1 -> B2
	tB1 := putBlobAndTree(t, ctx, database, casStorage, map[string]string{"b.txt": "b base\n"})
	respB1, _ := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{Author: "bob", Description: "B1", RootTreeHash: tB1})
	clB1 := respB1.ClId

	tB2 := putBlobAndTree(t, ctx, database, casStorage, map[string]string{"b.txt": "b base\n", "b2.txt": "b2\n"})
	respB2, _ := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{Author: "bob2", Description: "B2", ParentCl: clB1, RootTreeHash: tB2})
	clB2 := respB2.ClId

	// Concurrently re-shelve A1 and B1 with disjoint additions
	tA1Up := putBlobAndTree(t, ctx, database, casStorage, map[string]string{"a.txt": "a base\na extra\n"})
	tB1Up := putBlobAndTree(t, ctx, database, casStorage, map[string]string{"b.txt": "b base\nb extra\n"})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{ClId: clA1, Author: "alice", Description: "A1 up", RootTreeHash: tA1Up})
	}()
	go func() {
		defer wg.Done()
		_, _ = client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{ClId: clB1, Author: "bob", Description: "B1 up", RootTreeHash: tB1Up})
	}()
	wg.Wait()

	// Concurrently invoke SyncWorkspace on A2 and B2
	wg.Add(2)
	var syncRespA, syncRespB *nieblav1.SyncWorkspaceResponse
	var errA, errB error
	go func() {
		defer wg.Done()
		syncRespA, errA = client.SyncWorkspace(ctx, &nieblav1.SyncWorkspaceRequest{ClId: clA2})
	}()
	go func() {
		defer wg.Done()
		syncRespB, errB = client.SyncWorkspace(ctx, &nieblav1.SyncWorkspaceRequest{ClId: clB2})
	}()
	wg.Wait()

	if errA != nil || errB != nil {
		t.Fatalf("parallel SyncWorkspace failed: errA=%v, errB=%v", errA, errB)
	}
	if syncRespA.NeedsRebase || syncRespB.NeedsRebase {
		t.Errorf("parallel stacks experienced unexpected rebase conflict")
	}

	server.RebaseQueue().WaitIdle()
}
