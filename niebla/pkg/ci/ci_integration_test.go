package ci_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"niebla/internal/cli"
	"niebla/internal/config"
	"niebla/internal/db"
	internalgrpc "niebla/internal/grpc"
	"niebla/internal/web/handlers"
	"niebla/pkg/cas"
	pkgevents "niebla/pkg/events"
	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func setupTestEnvironment(t *testing.T) (*db.DB, *cas.Storage, string, string, func()) {
	t.Helper()
	ctx := context.Background()

	// 1. PostgreSQL Testcontainer
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("niebla_ci_integ_test"),
		postgres.WithUsername("niebla_ci_user"),
		postgres.WithPassword("niebla_ci_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to get connection string: %v", err)
	}

	database, err := db.Connect(ctx, db.DefaultConfig(connStr))
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to connect to db: %v", err)
	}

	// 2. CAS Storage
	casDir := filepath.Join(t.TempDir(), "cas")
	casStorage, err := cas.NewStorage(casDir)
	if err != nil {
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to create CAS storage: %v", err)
	}

	// 3. gRPC Server
	server, err := internalgrpc.NewServer(database, casStorage, 1000)
	if err != nil {
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to create server: %v", err)
	}

	trueVal := true
	cfg := &config.Config{
		Critique: config.CritiqueConfig{
			RequireGreenCheckRuns: &trueVal,
		},
	}
	server.SetToolchainConfig(cfg)
	server.SetEmitter(pkgevents.NewRecordingEmitter())

	grpcListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to listen on random port: %v", err)
	}
	grpcServer := grpc.NewServer()
	nieblav1.RegisterNieblaServiceServer(grpcServer, server)
	go func() {
		_ = grpcServer.Serve(grpcListener)
	}()

	// 4. Cotejo HTTP Handlers (Check Runs API)
	ciHandler := handlers.NewCIHandler(database)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/ci/status", ciHandler.HandlePostCheckRunStatus)
	mux.HandleFunc("GET /api/v1/ci/status", ciHandler.HandleGetCheckRuns)
	httpServer := httptest.NewServer(mux)

	cleanup := func() {
		httpServer.Close()
		grpcServer.Stop()
		grpcListener.Close()
		database.Close()
		_ = pgContainer.Terminate(context.Background())
	}

	return database, casStorage, grpcListener.Addr().String(), httpServer.URL, cleanup
}

func putBlob(t *testing.T, s *cas.Storage, database *db.DB, content []byte) ([]byte, int64) {
	t.Helper()
	b, err := s.Put(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("cas Put: %v", err)
	}
	if err := database.InsertBlob(context.Background(), b.HashBytes, b.Size, b.StoragePath); err != nil {
		t.Fatalf("InsertBlob: %v", err)
	}
	return b.HashBytes, b.Size
}

func putTree(t *testing.T, database *db.DB, entries []tree.Entry) []byte {
	t.Helper()
	tree.SortEntries(entries)
	h := tree.ComputeTreeHash(entries)
	if err := database.InsertTreeNodes(context.Background(), h, entries); err != nil {
		t.Fatalf("InsertTreeNodes: %v", err)
	}
	return h
}

func TestCILifecycle_Shelve_Exec_Status_Submit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	database, casStorage, grpcAddr, httpURL, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Shelve CL 2
	bHash, bSize := putBlob(t, casStorage, database, []byte("func TestMain() {}\n"))
	rootTree := putTree(t, database, []tree.Entry{
		{Name: "app_test.go", Mode: 0100644, Size: uint64(bSize), BlobSHA256: bHash},
	})

	conn, err := grpc.Dial(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.Dial: %v", err)
	}
	defer conn.Close()
	client := nieblav1.NewNieblaServiceClient(conn)

	shelveResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "ci_tester",
		Description:  "Feature tested with CI",
		RootTreeHash: rootTree,
		ParentCl:     1,
	})
	if err != nil {
		t.Fatalf("ShelveCL: %v", err)
	}
	clID := shelveResp.ClId

	// 2. Initial submit attempt -> fails because green check runs are required
	_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID})
	if err == nil {
		t.Fatalf("expected SubmitCL to fail with 0 check runs")
	}

	// 3. Execute `niebla ci exec` with a passing command
	rootCmd := cli.NewRootCmd()
	var outBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&outBuf)
	rootCmd.SetArgs([]string{
		"ci", "exec",
		"--server", grpcAddr,
		"--server-url", httpURL,
		"--cl", "2",
		"--runner", "local-tests",
		"--", "echo", "PASS: all 42 tests succeeded",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("niebla ci exec failed: %v, output: %s", err, outBuf.String())
	}

	// 4. Verify check run is now recorded as 'passed' in DB
	runs, err := database.ListCheckRunsForPatchset(ctx, clID, 1)
	if err != nil {
		t.Fatalf("ListCheckRunsForPatchset: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "passed" {
		t.Fatalf("expected 1 passed check run, got: %+v", runs)
	}

	// 5. Submit CL 2 -> succeeds cleanly!
	submitResp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID})
	if err != nil {
		t.Fatalf("expected SubmitCL to succeed after green check run: %v", err)
	}
	if submitResp.ClId != clID {
		t.Errorf("expected CL %d, got %d", clID, submitResp.ClId)
	}

	// 6. Test failing command with CL 3
	clHash := sha256.Sum256([]byte("dummy content"))
	clID3, err := database.CreateShelvedCL(ctx, "dev", "Broken feature", clHash[:], 2)
	if err != nil {
		t.Fatalf("CreateShelvedCL: %v", err)
	}

	failCmd := cli.NewRootCmd()
	failCmd.SetOut(&outBuf)
	failCmd.SetErr(&outBuf)
	failCmd.SetArgs([]string{
		"ci", "exec",
		"--server", grpcAddr,
		"--server-url", httpURL,
		"--cl", "3",
		"--runner", "local-tests",
		"--", "sh", "-c", "echo 'FAIL: test crashed' >&2; exit 1",
	})
	if err := failCmd.Execute(); err == nil {
		t.Fatalf("expected niebla ci exec to return error for failing command")
	}

	// 7. Verify check run recorded as 'failed'
	runs3, err := database.ListCheckRunsForPatchset(ctx, clID3, 1)
	if err != nil || len(runs3) != 1 || runs3[0].Status != "failed" {
		t.Fatalf("expected 1 failed check run for CL 3, got: %+v (err: %v)", runs3, err)
	}

	// 8. Normal submit fails
	_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID3})
	if err == nil {
		t.Fatalf("expected normal SubmitCL to fail for CL 3 with failed checks")
	}

	// 9. Force submit succeeds
	forceResp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID3, Force: true})
	if err != nil {
		t.Fatalf("expected Force SubmitCL to succeed: %v", err)
	}
	if forceResp.ClId != clID3 {
		t.Errorf("expected CL 3, got %d", forceResp.ClId)
	}
}
