package grpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"niebla/internal/config"
	"niebla/internal/db"
	"niebla/pkg/cas"
	"niebla/pkg/tree"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func setupTestEnvironment(t *testing.T) (nieblav1.NieblaServiceClient, *db.DB, *cas.Storage, *Server, func()) {
	t.Helper()
	ctx := context.Background()

	// 1. PostgreSQL Testcontainer
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("niebla_grpc_test"),
		postgres.WithUsername("niebla_grpc_user"),
		postgres.WithPassword("niebla_grpc_password"),
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
		t.Fatalf("failed to get connection string: %v", err)
	}

	database, err := db.Connect(ctx, db.DefaultConfig(connStr))
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to connect to test db: %v", err)
	}

	// 2. CAS Storage in temporary directory
	casDir, err := os.MkdirTemp("", "niebla-cas-grpc-*")
	if err != nil {
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to create CAS temp dir: %v", err)
	}

	casStorage, err := cas.NewStorage(casDir)
	if err != nil {
		os.RemoveAll(casDir)
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to create CAS storage: %v", err)
	}

	// 3. gRPC server with in-memory buffer listener
	serverInstance, err := NewServer(database, casStorage, 1000)
	if err != nil {
		os.RemoveAll(casDir)
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to create server instance: %v", err)
	}

	bufListener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	serverInstance.Register(grpcServer)

	go func() {
		_ = grpcServer.Serve(bufListener)
	}()

	// 4. gRPC client connecting over bufconn
	dialer := func(context.Context, string) (net.Conn, error) {
		return bufListener.Dial()
	}

	conn, err := grpc.DialContext(
		ctx,
		"bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		grpcServer.Stop()
		os.RemoveAll(casDir)
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to dial bufnet: %v", err)
	}

	client := nieblav1.NewNieblaServiceClient(conn)

	cleanup := func() {
		_ = conn.Close()
		grpcServer.GracefulStop()
		_ = bufListener.Close()
		os.RemoveAll(casDir)
		database.Close()
		_ = pgContainer.Terminate(context.Background())
	}

	return client, database, casStorage, serverInstance, cleanup
}

func TestNieblaServiceEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	client, database, casStorage, serverInstance, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// 1. Test GetHeadCL on initial repository
	headResp, err := client.GetHeadCL(ctx, &nieblav1.GetHeadCLRequest{})
	if err != nil {
		t.Fatalf("GetHeadCL failed: %v", err)
	}
	if headResp.ClId != 1 {
		t.Errorf("expected initial CL ID 1, got %d", headResp.ClId)
	}
	if !bytes.Equal(headResp.RootTreeHash, tree.EmptyTreeHash) {
		t.Errorf("expected initial root tree hash to equal EmptyTreeHash")
	}

	// 2. Test CreateWorkspace
	wsResp, err := client.CreateWorkspace(ctx, &nieblav1.CreateWorkspaceRequest{
		Name:    "my-workspace",
		Owner:   "eng-user",
		BaseCl:  0, // Should resolve to head CL 1
	})
	if err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}
	if wsResp.WorkspaceId == "" {
		t.Errorf("expected non-empty WorkspaceId")
	}
	if wsResp.BaseCl != 1 {
		t.Errorf("expected base CL 1, got %d", wsResp.BaseCl)
	}
	if !bytes.Equal(wsResp.RootTreeHash, tree.EmptyTreeHash) {
		t.Errorf("expected workspace root tree hash to match empty tree")
	}

	// 3. Test UploadBlob (streaming 150KB payload across multiple chunks)
	blobPayload := make([]byte, 150*1024)
	for i := range blobPayload {
		blobPayload[i] = byte(i % 251)
	}
	expectedBlobSHA := sha256.Sum256(blobPayload)

	uploadStream, err := client.UploadBlob(ctx)
	if err != nil {
		t.Fatalf("UploadBlob failed to open stream: %v", err)
	}

	// Send in 32KB chunks
	chunkSize := 32 * 1024
	for offset := 0; offset < len(blobPayload); offset += chunkSize {
		end := offset + chunkSize
		if end > len(blobPayload) {
			end = len(blobPayload)
		}
		if err := uploadStream.Send(&nieblav1.UploadBlobRequest{Data: blobPayload[offset:end]}); err != nil {
			t.Fatalf("failed to send upload chunk: %v", err)
		}
	}

	uploadResp, err := uploadStream.CloseAndRecv()
	if err != nil {
		t.Fatalf("UploadBlob CloseAndRecv failed: %v", err)
	}
	if !bytes.Equal(uploadResp.Sha256, expectedBlobSHA[:]) {
		t.Errorf("uploaded blob SHA-256 mismatch")
	}
	if uploadResp.Size != uint64(len(blobPayload)) {
		t.Errorf("uploaded blob size mismatch: expected %d, got %d", len(blobPayload), uploadResp.Size)
	}

	// Verify blob exists in CAS local storage
	if !casStorage.Exists(expectedBlobSHA[:]) {
		t.Fatalf("uploaded blob does not exist in CAS storage")
	}

	// 4. Test ReadBlob (full read)
	readStream, err := client.ReadBlob(ctx, &nieblav1.ReadBlobRequest{
		Sha256: expectedBlobSHA[:],
		Offset: 0,
		Length: 0,
	})
	if err != nil {
		t.Fatalf("ReadBlob failed: %v", err)
	}

	var downloadedData []byte
	for {
		chunk, err := readStream.Recv()
		if errorsIsEOF(err) {
			break
		}
		if err != nil {
			t.Fatalf("error receiving blob chunk: %v", err)
		}
		downloadedData = append(downloadedData, chunk.Data...)
	}
	if !bytes.Equal(downloadedData, blobPayload) {
		t.Fatalf("downloaded blob content does not match original payload")
	}

	// ReadBlob partial slice (offset: 100, length: 50)
	partialStream, err := client.ReadBlob(ctx, &nieblav1.ReadBlobRequest{
		Sha256: expectedBlobSHA[:],
		Offset: 100,
		Length: 50,
	})
	if err != nil {
		t.Fatalf("ReadBlob partial failed: %v", err)
	}
	var partialData []byte
	for {
		chunk, err := partialStream.Recv()
		if errorsIsEOF(err) {
			break
		}
		if err != nil {
			t.Fatalf("error receiving partial chunk: %v", err)
		}
		partialData = append(partialData, chunk.Data...)
	}
	if !bytes.Equal(partialData, blobPayload[100:150]) {
		t.Errorf("partial blob read content mismatch")
	}

	// ReadBlob error cases
	// Invalid SHA256 length
	invalidStream, err := client.ReadBlob(ctx, &nieblav1.ReadBlobRequest{Sha256: []byte{1, 2, 3}})
	if err == nil {
		_, err = invalidStream.Recv()
	}
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument for short sha256, got %v", err)
	}

	// Non-existent blob
	fakeHash := sha256.Sum256([]byte("fake non-existent blob"))
	nonExistentStream, err := client.ReadBlob(ctx, &nieblav1.ReadBlobRequest{Sha256: fakeHash[:]})
	if err == nil {
		_, err = nonExistentStream.Recv()
	}
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for non-existent blob, got %v", err)
	}

	// 5. Test ShelveCL and SubmitCL
	fileEntries := []tree.Entry{
		{
			Name:       "server.go",
			Mode:       0100644,
			Size:       uint64(len(blobPayload)),
			BlobSHA256: expectedBlobSHA[:],
		},
	}
	rootTreeHash := tree.ComputeTreeHash(fileEntries)
	if err := database.InsertTreeNodes(ctx, rootTreeHash, fileEntries); err != nil {
		t.Fatalf("InsertTreeNodes failed: %v", err)
	}

	shelveResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		WorkspaceId:  wsResp.WorkspaceId,
		Author:       "eng-user",
		Description:  "Add server.go",
		RootTreeHash: rootTreeHash,
		ParentCl:     1,
	})
	if err != nil {
		t.Fatalf("ShelveCL failed: %v", err)
	}
	if shelveResp.ClId <= 1 {
		t.Errorf("expected new CL ID > 1, got %d", shelveResp.ClId)
	}

	// Submit the CL
	submitResp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: shelveResp.ClId})
	if err != nil {
		t.Fatalf("SubmitCL failed: %v", err)
	}
	if submitResp.ClId != shelveResp.ClId {
		t.Errorf("submitted CL ID mismatch")
	}
	if submitResp.CommittedAtUnix <= 0 {
		t.Errorf("expected positive committed_at timestamp")
	}

	// Verify GetHeadCL is updated to the newly submitted CL
	newHead, err := client.GetHeadCL(ctx, &nieblav1.GetHeadCLRequest{})
	if err != nil {
		t.Fatalf("GetHeadCL after submit failed: %v", err)
	}
	if newHead.ClId != shelveResp.ClId {
		t.Errorf("expected head CL %d, got %d", shelveResp.ClId, newHead.ClId)
	}
	if !bytes.Equal(newHead.RootTreeHash, rootTreeHash) {
		t.Errorf("head root tree hash mismatch")
	}

	// 6. Test GetTree (with LRU cache verification)
	treeResp, err := client.GetTree(ctx, &nieblav1.GetTreeRequest{TreeHash: rootTreeHash})
	if err != nil {
		t.Fatalf("GetTree failed: %v", err)
	}
	if len(treeResp.Entries) != 1 || treeResp.Entries[0].Name != "server.go" {
		t.Fatalf("unexpected tree entries: %+v", treeResp.Entries)
	}

	// Call again to verify LRU cache hit
	cachedTreeResp, err := client.GetTree(ctx, &nieblav1.GetTreeRequest{TreeHash: rootTreeHash})
	if err != nil {
		t.Fatalf("cached GetTree failed: %v", err)
	}
	if len(cachedTreeResp.Entries) != 1 || cachedTreeResp.Entries[0].Name != "server.go" {
		t.Fatalf("cached tree entries mismatch: %+v", cachedTreeResp.Entries)
	}

	// GetTree with EmptyTreeHash
	emptyResp, err := client.GetTree(ctx, &nieblav1.GetTreeRequest{TreeHash: tree.EmptyTreeHash})
	if err != nil || len(emptyResp.Entries) != 0 {
		t.Errorf("expected empty entries for EmptyTreeHash, got %+v", emptyResp)
	}

	// 7. Test GetNode
	nodeResp, err := client.GetNode(ctx, &nieblav1.GetNodeRequest{
		ParentTreeHash: rootTreeHash,
		Name:           "server.go",
	})
	if err != nil {
		t.Fatalf("GetNode failed: %v", err)
	}
	if nodeResp.Name != "server.go" || !bytes.Equal(nodeResp.BlobSha256, expectedBlobSHA[:]) {
		t.Errorf("GetNode returned unexpected entry: %+v", nodeResp)
	}

	// GetNode missing entry
	_, err = client.GetNode(ctx, &nieblav1.GetNodeRequest{
		ParentTreeHash: rootTreeHash,
		Name:           "missing.go",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound for missing node, got %v", err)
	}

	// 8. Test ShelveCL with Reviewers and SubmitCL guards
	t.Log("Testing ShelveCL with Reviewers and SubmitCL guards")

	// Set strict policy: allow_submit_with_unresolved_comments = false, required_approvals = 1
	strictCfg := config.DefaultConfig()
	strictCfg.Critique.Auth.Mode = "headers"
	allowUnresolved := false
	strictCfg.Critique.AllowSubmitWithUnresolvedComments = &allowUnresolved
	reqApprovals := 1
	strictCfg.Critique.RequiredApprovals = &reqApprovals
	serverInstance.SetToolchainConfig(strictCfg)

	// Shelve a new CL with reviewers: alice and bob
	shelveResp2, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		WorkspaceId:  wsResp.WorkspaceId,
		Author:       "eng-user",
		Description:  "CL with reviewers and guards",
		RootTreeHash: rootTreeHash,
		ParentCl:     shelveResp.ClId,
		Reviewers:    []string{"alice", "bob"},
	})
	if err != nil {
		t.Fatalf("ShelveCL with reviewers failed: %v", err)
	}

	// Verify reviewers are recorded in database
	assignedReviewers, err := database.ListReviewers(ctx, shelveResp2.ClId)
	if err != nil {
		t.Fatalf("ListReviewers failed: %v", err)
	}
	if len(assignedReviewers) != 2 {
		t.Errorf("expected 2 assigned reviewers, got %d", len(assignedReviewers))
	}

	// Add an unresolved comment
	createdComment, err := database.CreateComment(ctx, shelveResp2.ClId, 1, "server.go", 10, 10, "modified", "alice", "Needs refactoring", nil, false)
	if err != nil {
		t.Fatalf("CreateComment failed: %v", err)
	}

	// Attempt submit without force -> must fail due to unresolved comment
	_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: shelveResp2.ClId, Force: false})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for unresolved comments, got code %v: %v", status.Code(err), err)
	}

	// Resolve the comment
	err = database.ResolveComment(ctx, createdComment.ID, true)
	if err != nil {
		t.Fatalf("ResolveComment failed: %v", err)
	}

	// Attempt submit without force -> must fail due to missing required approvals (0 of 1)
	_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: shelveResp2.ClId, Force: false})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition for missing approvals, got code %v: %v", status.Code(err), err)
	}

	// Submit with force = true -> bypasses guards and succeeds
	forcedResp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: shelveResp2.ClId, Force: true})
	if err != nil {
		t.Fatalf("SubmitCL with force failed: %v", err)
	}
	if forcedResp.ClId != shelveResp2.ClId {
		t.Errorf("forced submit CL mismatch")
	}
}

func errorsIsEOF(err error) bool {
	return err == io.EOF
}

