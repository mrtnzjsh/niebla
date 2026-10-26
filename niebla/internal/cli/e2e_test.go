package cli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"niebla/internal/cli/config"
	"niebla/internal/db"
	internalgrpc "niebla/internal/grpc"
	"niebla/pkg/cas"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
)

func setupTestServer(t *testing.T) (string, func()) {
	t.Helper()
	ctx := context.Background()

	// 1. PostgreSQL Testcontainer
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("niebla_cli_test"),
		postgres.WithUsername("niebla_cli_user"),
		postgres.WithPassword("niebla_cli_password"),
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
	casDir, err := os.MkdirTemp("", "niebla-cas-cli-*")
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

	// 3. gRPC server on TCP port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.RemoveAll(casDir)
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to listen on port: %v", err)
	}

	grpcServer := grpc.NewServer()
	repoServer, err := internalgrpc.NewServer(database, casStorage, 1000)
	if err != nil {
		_ = listener.Close()
		os.RemoveAll(casDir)
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to create repo server: %v", err)
	}
	repoServer.Register(grpcServer)

	go func() {
		_ = grpcServer.Serve(listener)
	}()

	cleanup := func() {
		grpcServer.GracefulStop()
		_ = listener.Close()
		os.RemoveAll(casDir)
		database.Close()
		_ = pgContainer.Terminate(context.Background())
	}

	return listener.Addr().String(), cleanup
}

func runCLI(args ...string) (string, error) {
	rootCmd := NewRootCmd()
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs(args)

	err := rootCmd.Execute()
	combined := outBuf.String() + errBuf.String()
	return combined, err
}

func TestNieblaCLIEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	serverAddr, cleanup := setupTestServer(t)
	defer cleanup()

	tempDir, err := os.MkdirTemp("", "niebla-e2e-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfgDir := filepath.Join(tempDir, "config")
	mountDir := filepath.Join(tempDir, "mount", "test-ws")

	// 1. Workspace Create
	out, err := runCLI("workspace", "create", "test-ws",
		"--mount-dir", mountDir,
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("workspace create failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, `Workspace "test-ws" created successfully`) {
		t.Fatalf("unexpected create output: %s", out)
	}

	// Verify metadata file was created
	mgr, err := config.NewManager(cfgDir)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}
	ws, err := mgr.Load("test-ws")
	if err != nil {
		t.Fatalf("failed to load created workspace config: %v", err)
	}
	if ws.Name != "test-ws" || ws.BaseCL != 1 {
		t.Fatalf("unexpected workspace config values: %+v", ws)
	}

	// 2. Workspace List
	out, err = runCLI("workspace", "list", "--config-dir", cfgDir)
	if err != nil {
		t.Fatalf("workspace list failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "test-ws") || !strings.Contains(out, mountDir) {
		t.Fatalf("list output missing workspace: %s", out)
	}

	// 3. Status on clean scratch (initially empty)
	out, err = runCLI("status",
		"--workspace", "test-ws",
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("status failed on empty workspace: %v\nOutput: %s", err, out)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected empty status on clean workspace, got: %s", out)
	}

	// 4. Add a file in scratch
	scratchFile := filepath.Join(ws.ScratchDir, "hello.txt")
	if err := os.WriteFile(scratchFile, []byte("Hello Niebla World!\n"), 0644); err != nil {
		t.Fatalf("failed to write scratch file: %v", err)
	}

	// 5. Status should show [A] hello.txt
	out, err = runCLI("status",
		"--workspace", "test-ws",
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("status failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "A  hello.txt") {
		t.Fatalf("expected 'A  hello.txt' in status output: %s", out)
	}

	// 6. Diff should show additions
	out, err = runCLI("diff",
		"--workspace", "test-ws",
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("diff failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "+++ b/hello.txt") || !strings.Contains(out, "+Hello Niebla World!") {
		t.Fatalf("diff output missing added lines: %s", out)
	}

	// 7. Shelve changes
	out, err = runCLI("shelve",
		"-m", "Add hello.txt",
		"--workspace", "test-ws",
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("shelve failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "Shelved CL") || !strings.Contains(out, "Add hello.txt") {
		t.Fatalf("unexpected shelve output: %s", out)
	}

	// Verify ws.ActiveCL is now set
	ws, err = mgr.Load("test-ws")
	if err != nil {
		t.Fatalf("failed to reload workspace: %v", err)
	}
	if ws.ActiveCL <= 1 {
		t.Fatalf("expected ActiveCL > 1, got %d", ws.ActiveCL)
	}
	shelvedCLID := ws.ActiveCL

	// 8. Submit CL
	out, err = runCLI("submit",
		"--workspace", "test-ws",
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("submit failed: %v\nOutput: %s", err, out)
	}
	expectedSubmitMsg := fmt.Sprintf("Successfully submitted CL %d at revision %d", shelvedCLID, shelvedCLID)
	if !strings.Contains(out, expectedSubmitMsg) {
		t.Fatalf("unexpected submit output: %s", out)
	}

	// Verify workspace active CL cleared and base CL updated
	ws, err = mgr.Load("test-ws")
	if err != nil {
		t.Fatalf("failed to reload workspace after submit: %v", err)
	}
	if ws.ActiveCL != 0 {
		t.Errorf("expected ActiveCL to be reset to 0, got %d", ws.ActiveCL)
	}
	if ws.BaseCL != shelvedCLID {
		t.Errorf("expected BaseCL %d, got %d", shelvedCLID, ws.BaseCL)
	}

	// 9. Verify trunk head updated on server
	ctx := context.Background()
	conn, client, err := DialGRPC(ctx, serverAddr)
	if err != nil {
		t.Fatalf("failed to dial server: %v", err)
	}
	defer conn.Close()

	headResp, err := client.GetHeadCL(ctx, &nieblav1.GetHeadCLRequest{})
	if err != nil {
		t.Fatalf("GetHeadCL failed: %v", err)
	}
	if headResp.ClId != shelvedCLID {
		t.Errorf("expected head CL %d, got %d", shelvedCLID, headResp.ClId)
	}

	// 10. Workspace Delete
	out, err = runCLI("workspace", "delete", "test-ws",
		"--force",
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("workspace delete failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, `Workspace "test-ws" deleted successfully`) {
		t.Fatalf("unexpected delete output: %s", out)
	}

	// Verify list is now empty
	out, err = runCLI("workspace", "list", "--config-dir", cfgDir)
	if err != nil {
		t.Fatalf("workspace list after delete failed: %v", err)
	}
	if !strings.Contains(out, "No Niebla workspaces found") {
		t.Fatalf("expected no workspaces found, got: %s", out)
	}
}
