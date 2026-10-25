package db

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"citc-toolchain/pkg/tree"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestDB(t *testing.T) (*DB, func()) {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("citc_test"),
		postgres.WithUsername("citc_user"),
		postgres.WithPassword("citc_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres test container: %v", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to get container connection string: %v", err)
	}

	cfg := DefaultConfig(connStr)
	database, err := Connect(ctx, cfg)
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to connect to test database and run migrations: %v", err)
	}

	cleanup := func() {
		database.Close()
		_ = pgContainer.Terminate(context.Background())
	}

	return database, cleanup
}

func TestDatabaseOperationsWithTestcontainer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	ctx := context.Background()
	database, cleanup := setupTestDB(t)
	defer cleanup()

	// 1. Validate Initial Seed Changelist
	headCL, err := database.GetHeadCL(ctx)
	if err != nil {
		t.Fatalf("GetHeadCL failed on seeded database: %v", err)
	}
	if headCL.CLID != 1 {
		t.Errorf("expected head CLID 1, got %d", headCL.CLID)
	}
	if headCL.Status != "submitted" {
		t.Errorf("expected head status 'submitted', got %q", headCL.Status)
	}
	if !bytes.Equal(headCL.RootTreeHash, tree.EmptyTreeHash) {
		t.Errorf("expected initial root_tree_hash to match empty tree hash")
	}

	// 2. Validate Blobs Operations
	sampleBlobData := []byte("content of sample file for citc")
	blobHashArr := sha256.Sum256(sampleBlobData)
	blobHash := blobHashArr[:]
	blobSize := int64(len(sampleBlobData))
	storagePath := "/devpool/citc_blobs/ab/cd/sampleblob"

	err = database.InsertBlob(ctx, blobHash, blobSize, storagePath)
	if err != nil {
		t.Fatalf("InsertBlob failed: %v", err)
	}

	// Test ON CONFLICT DO NOTHING
	err = database.InsertBlob(ctx, blobHash, blobSize, storagePath)
	if err != nil {
		t.Fatalf("InsertBlob duplicate failed: %v", err)
	}

	// GetBlob
	record, err := database.GetBlob(ctx, blobHash)
	if err != nil {
		t.Fatalf("GetBlob failed: %v", err)
	}
	if !bytes.Equal(record.SHA256, blobHash) {
		t.Errorf("blob hash mismatch")
	}
	if record.ByteSize != blobSize {
		t.Errorf("expected size %d, got %d", blobSize, record.ByteSize)
	}
	if record.StoragePath != storagePath {
		t.Errorf("expected path %s, got %s", storagePath, record.StoragePath)
	}

	// GetBlob Not Found
	missingHash := sha256.Sum256([]byte("non-existent blob"))
	_, err = database.GetBlob(ctx, missingHash[:])
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing blob, got %v", err)
	}

	// 3. Validate Tree Nodes Operations
	testEntries := []tree.Entry{
		{
			Name:       "main.go",
			Mode:       0100644,
			Size:       uint64(blobSize),
			BlobSHA256: blobHash,
		},
		{
			Name:        "pkg",
			Mode:        0040755,
			Size:        0,
			SubTreeHash: tree.EmptyTreeHash,
		},
	}
	testTreeHash := tree.ComputeTreeHash(testEntries)

	err = database.InsertTreeNodes(ctx, testTreeHash, testEntries)
	if err != nil {
		t.Fatalf("InsertTreeNodes failed: %v", err)
	}

	// Retrieve Tree Entries
	fetchedEntries, err := database.GetTreeEntries(ctx, testTreeHash)
	if err != nil {
		t.Fatalf("GetTreeEntries failed: %v", err)
	}
	if len(fetchedEntries) != 2 {
		t.Fatalf("expected 2 tree entries, got %d", len(fetchedEntries))
	}
	if fetchedEntries[0].Name != "main.go" || fetchedEntries[1].Name != "pkg" {
		t.Errorf("unexpected tree entries ordering: %+v", fetchedEntries)
	}

	// Retrieve Single Node Entry
	nodeEntry, err := database.GetNodeEntry(ctx, testTreeHash, "main.go")
	if err != nil {
		t.Fatalf("GetNodeEntry failed: %v", err)
	}
	if nodeEntry.Name != "main.go" || !bytes.Equal(nodeEntry.BlobSHA256, blobHash) {
		t.Errorf("node entry mismatch: %+v", nodeEntry)
	}

	// Node Not Found
	_, err = database.GetNodeEntry(ctx, testTreeHash, "does_not_exist.txt")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}

	// 4. Validate Changelist Operations
	// Create Shelved CL
	shelvedCLID, err := database.CreateShelvedCL(ctx, "developer@google.com", "Add main.go and pkg dir", testTreeHash, headCL.CLID)
	if err != nil {
		t.Fatalf("CreateShelvedCL failed: %v", err)
	}
	if shelvedCLID <= headCL.CLID {
		t.Errorf("expected shelved CLID > %d, got %d", headCL.CLID, shelvedCLID)
	}

	shelvedCL, err := database.GetCL(ctx, shelvedCLID)
	if err != nil {
		t.Fatalf("GetCL failed: %v", err)
	}
	if shelvedCL.Status != "shelved" {
		t.Errorf("expected status 'shelved', got %q", shelvedCL.Status)
	}
	if *shelvedCL.ParentCL != headCL.CLID {
		t.Errorf("expected parent CL %d, got %d", headCL.CLID, *shelvedCL.ParentCL)
	}

	// Head should still be CL 1 because shelvedCL is not yet submitted
	currentHead, err := database.GetHeadCL(ctx)
	if err != nil || currentHead.CLID != 1 {
		t.Fatalf("expected head CL to remain 1 before submit, got %v", currentHead)
	}

	// Submit CL
	submittedCL, err := database.SubmitCL(ctx, shelvedCLID)
	if err != nil {
		t.Fatalf("SubmitCL failed: %v", err)
	}
	if submittedCL.Status != "submitted" {
		t.Errorf("expected status 'submitted', got %q", submittedCL.Status)
	}

	// Now head should be the newly submitted CL
	newHead, err := database.GetHeadCL(ctx)
	if err != nil {
		t.Fatalf("GetHeadCL failed: %v", err)
	}
	if newHead.CLID != shelvedCLID {
		t.Errorf("expected new head CLID %d, got %d", shelvedCLID, newHead.CLID)
	}

	// Submitting already submitted CL should be idempotent
	idemCL, err := database.SubmitCL(ctx, shelvedCLID)
	if err != nil || idemCL.Status != "submitted" {
		t.Errorf("idempotent submit failed: %v", err)
	}

	// 5. Validate Workspace Operations
	ws, rootHash, err := database.CreateWorkspace(ctx, "my-feature-workspace", "developer", 0)
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if ws.ID == "" {
		t.Errorf("expected non-empty workspace UUID")
	}
	if *ws.BaseCL != newHead.CLID {
		t.Errorf("expected workspace base_cl to default to head %d, got %d", newHead.CLID, *ws.BaseCL)
	}
	if !bytes.Equal(rootHash, newHead.RootTreeHash) {
		t.Errorf("workspace rootTreeHash mismatch")
	}

	// GetWorkspace
	fetchedWS, err := database.GetWorkspace(ctx, ws.ID)
	if err != nil {
		t.Fatalf("GetWorkspace failed: %v", err)
	}
	if fetchedWS.Name != "my-feature-workspace" || fetchedWS.Owner != "developer" {
		t.Errorf("fetched workspace mismatch: %+v", fetchedWS)
	}

	// Get missing workspace
	_, err = database.GetWorkspace(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing workspace, got %v", err)
	}

	// 6. Validate Per-Reviewer File Review Operations
	// Alice reviews main.go
	aliceReviewed, err := database.ListReviewedFiles(ctx, 1, "alice")
	if err != nil {
		t.Fatalf("ListReviewedFiles failed: %v", err)
	}
	if len(aliceReviewed) != 0 {
		t.Errorf("expected 0 reviewed files for alice initially, got %d", len(aliceReviewed))
	}

	err = database.MarkFileReviewed(ctx, 1, "main.go", "alice", true)
	if err != nil {
		t.Fatalf("MarkFileReviewed failed: %v", err)
	}

	aliceReviewed, err = database.ListReviewedFiles(ctx, 1, "alice")
	if err != nil || !aliceReviewed["main.go"] {
		t.Errorf("expected main.go to be reviewed for alice, got %v", aliceReviewed)
	}

	// Bob should still have 0 reviewed files (per-reviewer isolation)
	bobReviewed, err := database.ListReviewedFiles(ctx, 1, "bob")
	if err != nil {
		t.Fatalf("ListReviewedFiles for bob failed: %v", err)
	}
	if bobReviewed["main.go"] {
		t.Errorf("expected main.go NOT to be reviewed for bob")
	}

	// Bob reviews main.go
	err = database.MarkFileReviewed(ctx, 1, "main.go", "bob", true)
	if err != nil {
		t.Fatalf("MarkFileReviewed for bob failed: %v", err)
	}
	bobReviewed, err = database.ListReviewedFiles(ctx, 1, "bob")
	if err != nil || !bobReviewed["main.go"] {
		t.Errorf("expected main.go to be reviewed for bob, got %v", bobReviewed)
	}

	// Alice unmarks main.go
	err = database.MarkFileReviewed(ctx, 1, "main.go", "alice", false)
	if err != nil {
		t.Fatalf("unmarking file reviewed failed: %v", err)
	}
	aliceReviewed, _ = database.ListReviewedFiles(ctx, 1, "alice")
	if aliceReviewed["main.go"] {
		t.Errorf("expected main.go to no longer be reviewed for alice")
	}
	// Bob should still have main.go reviewed
	bobReviewed, _ = database.ListReviewedFiles(ctx, 1, "bob")
	if !bobReviewed["main.go"] {
		t.Errorf("expected main.go to remain reviewed for bob")
	}

	// 7. Validate Draft Comments & Publishing Operations
	// Alice creates two draft comments
	draft1, err := database.CreateComment(ctx, 1, 1, "main.go", 10, 10, "modified", "alice", "Consider extracting helper", nil, true)
	if err != nil {
		t.Fatalf("CreateComment draft1 failed: %v", err)
	}
	if !draft1.IsDraft {
		t.Errorf("expected draft1 to have IsDraft=true")
	}

	draft2, err := database.CreateComment(ctx, 1, 1, "main.go", 20, 25, "modified", "alice", "Check error return", nil, true)
	if err != nil {
		t.Fatalf("CreateComment draft2 failed: %v", err)
	}
	if !draft2.IsDraft {
		t.Errorf("expected draft2 to have IsDraft=true")
	}

	// Verify Alice has 2 drafts
	countAlice, err := database.CountDraftComments(ctx, 1, "alice")
	if err != nil {
		t.Fatalf("CountDraftComments failed: %v", err)
	}
	if countAlice != 2 {
		t.Errorf("expected 2 drafts for alice, got %d", countAlice)
	}

	// Bob should have 0 drafts
	countBob, err := database.CountDraftComments(ctx, 1, "bob")
	if err != nil || countBob != 0 {
		t.Errorf("expected 0 drafts for bob, got %d", countBob)
	}

	// List Alice's drafts
	aliceDrafts, err := database.ListDraftComments(ctx, 1, "alice")
	if err != nil {
		t.Fatalf("ListDraftComments failed: %v", err)
	}
	if len(aliceDrafts) != 2 {
		t.Errorf("expected 2 drafts in list, got %d", len(aliceDrafts))
	}

	// Alice discards draft2
	err = database.DiscardDraftComment(ctx, draft2.ID, "alice")
	if err != nil {
		t.Fatalf("DiscardDraftComment failed: %v", err)
	}
	countAliceAfterDiscard, _ := database.CountDraftComments(ctx, 1, "alice")
	if countAliceAfterDiscard != 1 {
		t.Errorf("expected 1 draft after discard, got %d", countAliceAfterDiscard)
	}

	// Alice publishes her remaining drafts
	publishedCount, err := database.PublishDraftComments(ctx, 1, "alice")
	if err != nil {
		t.Fatalf("PublishDraftComments failed: %v", err)
	}
	if publishedCount != 1 {
		t.Errorf("expected 1 published comment, got %d", publishedCount)
	}

	// Count should now be 0
	countAliceAfterPublish, _ := database.CountDraftComments(ctx, 1, "alice")
	if countAliceAfterPublish != 0 {
		t.Errorf("expected 0 drafts after publish, got %d", countAliceAfterPublish)
	}

	// Verify draft1 is now published
	publishedComment, err := database.GetComment(ctx, draft1.ID)
	if err != nil {
		t.Fatalf("GetComment failed: %v", err)
	}
	if publishedComment.IsDraft {
		t.Errorf("expected publishedComment to have IsDraft=false")
	}

	// 8. Test CL Reviewers and Counts
	t.Log("Testing CL Reviewers and Counts")

	// 8a. Add reviewers to CL 1
	err = database.AddReviewers(ctx, 1, []string{"alice", "bob", "carol"})
	if err != nil {
		t.Fatalf("AddReviewers failed: %v", err)
	}

	// Idempotent add with duplicates and whitespace
	err = database.AddReviewers(ctx, 1, []string{"alice", "  bob  ", ""})
	if err != nil {
		t.Fatalf("AddReviewers duplicate failed: %v", err)
	}

	// 8b. List reviewers - all should have 'pending' verdict initially
	reviewers, err := database.ListReviewers(ctx, 1)
	if err != nil {
		t.Fatalf("ListReviewers failed: %v", err)
	}
	if len(reviewers) != 3 {
		t.Fatalf("expected 3 reviewers, got %d", len(reviewers))
	}
	for _, r := range reviewers {
		if r.Verdict != "pending" {
			t.Errorf("expected reviewer %s to have verdict 'pending', got %q", r.Reviewer, r.Verdict)
		}
	}

	// 8c. Alice grants LGTM approval
	err = database.SetApproval(ctx, 1, "alice", "lgtm")
	if err != nil {
		t.Fatalf("SetApproval failed: %v", err)
	}

	// Dave (not explicitly pre-assigned) requests changes
	err = database.SetApproval(ctx, 1, "dave", "changes_requested")
	if err != nil {
		t.Fatalf("SetApproval for dave failed: %v", err)
	}

	// 8d. List reviewers again - should include Alice (lgtm), Bob (pending), Carol (pending), Dave (changes_requested)
	reviewersAfter, err := database.ListReviewers(ctx, 1)
	if err != nil {
		t.Fatalf("ListReviewers after approvals failed: %v", err)
	}
	if len(reviewersAfter) != 4 {
		t.Fatalf("expected 4 reviewers, got %d", len(reviewersAfter))
	}
	reviewerMap := make(map[string]string)
	for _, r := range reviewersAfter {
		reviewerMap[r.Reviewer] = r.Verdict
	}
	if reviewerMap["alice"] != "lgtm" {
		t.Errorf("expected alice to have verdict 'lgtm', got %q", reviewerMap["alice"])
	}
	if reviewerMap["bob"] != "pending" {
		t.Errorf("expected bob to have verdict 'pending', got %q", reviewerMap["bob"])
	}
	if reviewerMap["dave"] != "changes_requested" {
		t.Errorf("expected dave to have verdict 'changes_requested', got %q", reviewerMap["dave"])
	}

	// 8e. Remove Carol
	err = database.RemoveReviewer(ctx, 1, "carol")
	if err != nil {
		t.Fatalf("RemoveReviewer failed: %v", err)
	}
	reviewersAfterRemove, err := database.ListReviewers(ctx, 1)
	if err != nil {
		t.Fatalf("ListReviewers after remove failed: %v", err)
	}
	if len(reviewersAfterRemove) != 3 {
		t.Fatalf("expected 3 reviewers after remove, got %d", len(reviewersAfterRemove))
	}

	// 8f. CountApprovals
	approvedCount, err := database.CountApprovals(ctx, 1)
	if err != nil {
		t.Fatalf("CountApprovals failed: %v", err)
	}
	if approvedCount != 1 {
		t.Errorf("expected 1 approved reviewer (alice), got %d", approvedCount)
	}

	// 8g. CountUnresolvedComments
	// draft1 is published and currently unresolved
	unresolvedCount, err := database.CountUnresolvedComments(ctx, 1)
	if err != nil {
		t.Fatalf("CountUnresolvedComments failed: %v", err)
	}
	if unresolvedCount != 1 {
		t.Errorf("expected 1 unresolved comment, got %d", unresolvedCount)
	}

	// Mark draft1 as resolved
	err = database.ResolveComment(ctx, draft1.ID, true)
	if err != nil {
		t.Fatalf("ResolveComment failed: %v", err)
	}

	unresolvedCountAfterResolve, err := database.CountUnresolvedComments(ctx, 1)
	if err != nil {
		t.Fatalf("CountUnresolvedComments after resolve failed: %v", err)
	}
	if unresolvedCountAfterResolve != 0 {
		t.Errorf("expected 0 unresolved comments after resolve, got %d", unresolvedCountAfterResolve)
	}
}

