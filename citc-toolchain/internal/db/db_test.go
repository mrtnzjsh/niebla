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
}
