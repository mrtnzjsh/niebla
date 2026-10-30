package grpc

import (
	"context"
	"testing"
	"time"

	nieblav1 "niebla/pkg/proto/nieblav1"
)

func TestAsyncCascadeRebase_ShelveReturnsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, server, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Create Base Parent Tree & CL
	baseTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"shared.txt": "initial line\n",
	})
	parentResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "alice",
		Description:  "parent v1",
		RootTreeHash: baseTree,
	})
	if err != nil {
		t.Fatalf("failed to create parent CL: %v", err)
	}
	parentCLID := parentResp.ClId

	// 2. Create Child CL on Parent
	childTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"shared.txt": "initial line\n",
		"child.txt":  "child line\n",
	})
	childResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "bob",
		Description:  "child CL",
		ParentCl:     parentCLID,
		RootTreeHash: childTree,
	})
	if err != nil {
		t.Fatalf("failed to create child CL: %v", err)
	}
	childCLID := childResp.ClId

	// 3. Update Parent CL (disjoint edit to shared.txt)
	updatedParentTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"shared.txt": "initial line\nparent addition\n",
	})

	start := time.Now()
	_, err = client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		ClId:         parentCLID,
		Author:       "alice",
		Description:  "parent v2",
		RootTreeHash: updatedParentTree,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("failed to update parent CL: %v", err)
	}

	// ShelveCL must return quickly without waiting for downstream tree merges
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected ShelveCL to return in <500ms, took %v", elapsed)
	}

	// Wait for background rebase worker to drain
	server.RebaseQueue().WaitIdle()

	// 4. Verify child was rebased in background
	childCL, err := database.GetCL(ctx, childCLID)
	if err != nil {
		t.Fatalf("failed to reload child CL: %v", err)
	}
	if childCL.NeedsRebase {
		t.Errorf("child should have rebased cleanly without conflicts")
	}

	// Query child tree entries to confirm 3-way merge result
	entries, err := server.getTreeEntries(ctx, childCL.RootTreeHash)
	if err != nil {
		t.Fatalf("failed to read child tree entries: %v", err)
	}
	entryMap := make(map[string]bool)
	for _, e := range entries {
		entryMap[e.Name] = true
	}
	if !entryMap["shared.txt"] || !entryMap["child.txt"] {
		t.Errorf("child tree missing entries after async cascade: %v", entryMap)
	}
}

func TestSyncThrough_OnDemandRebase(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, server, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Create Base Parent Tree & CL
	baseTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"file.txt": "base line 1\nbase line 2\n",
	})
	parentResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "alice",
		Description:  "parent v1",
		RootTreeHash: baseTree,
	})
	if err != nil {
		t.Fatalf("failed to create parent CL: %v", err)
	}
	parentCLID := parentResp.ClId

	// 2. Create Child CL on Parent (adds child_feature.txt)
	childTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"file.txt":          "base line 1\nbase line 2\n",
		"child_feature.txt": "feature active\n",
	})
	childResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "bob",
		Description:  "child CL",
		ParentCl:     parentCLID,
		RootTreeHash: childTree,
	})
	if err != nil {
		t.Fatalf("failed to create child CL: %v", err)
	}
	childCLID := childResp.ClId

	// 3. Pause rebase queue to simulate parent update not yet processed by worker
	// Update parent tree directly in database
	newParentTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"file.txt": "base line 1\nbase line 2\nupstream addition\n",
	})
	_ = database.UpdateCLRootTreeHash(ctx, parentCLID, newParentTree)
	_ = database.CreateAutoRebasePatchset(ctx, parentCLID, 2, newParentTree)

	// 4. Call SyncWorkspace on child CL immediately -> triggers SyncThrough!
	syncResp, err := client.SyncWorkspace(ctx, &nieblav1.SyncWorkspaceRequest{
		ClId: childCLID,
	})
	if err != nil {
		t.Fatalf("SyncWorkspace with sync-through failed: %v", err)
	}

	if syncResp.NeedsRebase {
		t.Errorf("expected clean sync-through without conflicts, got: %s", syncResp.RebaseConflictDetails)
	}
	if syncResp.PatchsetNumber < 2 {
		t.Errorf("expected patchset number >= 2 after sync-through rebase, got %d", syncResp.PatchsetNumber)
	}

	// Verify child tree now includes both upstream addition and child_feature.txt
	entries, err := server.getTreeEntries(ctx, syncResp.RootTreeHash)
	if err != nil {
		t.Fatalf("failed to get child tree entries: %v", err)
	}
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["file.txt"] || !names["child_feature.txt"] {
		t.Errorf("expected file.txt and child_feature.txt in child tree, got %v", names)
	}
}

func TestSyncThrough_ConflictHandling(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Create Base Parent
	baseTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"conflict.txt": "original line\n",
	})
	parentResp, _ := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "alice",
		Description:  "parent v1",
		RootTreeHash: baseTree,
	})
	parentCLID := parentResp.ClId

	// 2. Child modifies conflict.txt: "child edit\n"
	childTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"conflict.txt": "child edit\n",
	})
	childResp, _ := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "bob",
		Description:  "child CL",
		ParentCl:     parentCLID,
		RootTreeHash: childTree,
	})
	childCLID := childResp.ClId

	// 3. Parent modifies conflict.txt to conflicting content: "parent edit\n"
	newParentTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"conflict.txt": "parent edit\n",
	})
	_ = database.UpdateCLRootTreeHash(ctx, parentCLID, newParentTree)
	_ = database.CreateAutoRebasePatchset(ctx, parentCLID, 2, newParentTree)

	// 4. Call SyncWorkspace on child -> triggers SyncThrough and detects conflict
	syncResp, err := client.SyncWorkspace(ctx, &nieblav1.SyncWorkspaceRequest{
		ClId: childCLID,
	})
	if err != nil {
		t.Fatalf("SyncWorkspace failed: %v", err)
	}

	if !syncResp.NeedsRebase {
		t.Errorf("expected NeedsRebase = true on conflicting edits")
	}
	if syncResp.RebaseConflictDetails == "" {
		t.Errorf("expected conflict details in sync response")
	}
}
