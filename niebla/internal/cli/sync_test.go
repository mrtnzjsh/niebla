package cli

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"

	"niebla/internal/cli/config"
	"niebla/pkg/nieblafs/control"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"google.golang.org/grpc"
)

type mockNieblaSyncService struct {
	nieblav1.UnimplementedNieblaServiceServer
	syncResp *nieblav1.SyncWorkspaceResponse
	syncErr  error
}

func (m *mockNieblaSyncService) SyncWorkspace(ctx context.Context, req *nieblav1.SyncWorkspaceRequest) (*nieblav1.SyncWorkspaceResponse, error) {
	if m.syncErr != nil {
		return nil, m.syncErr
	}
	return m.syncResp, nil
}

type mockControlHandler struct {
	reanchorCalled bool
	lastTree       string
	lastCL         int64
	purged         int
}

func (m *mockControlHandler) HandlePing(ctx context.Context) error {
	return nil
}

func (m *mockControlHandler) HandleReanchor(ctx context.Context, newTree string, clID int64) (int, error) {
	m.reanchorCalled = true
	m.lastTree = newTree
	m.lastCL = clID
	return m.purged, nil
}

func (m *mockControlHandler) HandleInvalidate(ctx context.Context, paths []string) (int, error) {
	return len(paths), nil
}

func (m *mockControlHandler) HandleStatus(ctx context.Context) (control.Response, error) {
	return control.Response{Status: "ok"}, nil
}

func startMockGRPCServer(t *testing.T, service nieblav1.NieblaServiceServer) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on local tcp: %v", err)
	}

	srv := grpc.NewServer()
	nieblav1.RegisterNieblaServiceServer(srv, service)

	go func() {
		_ = srv.Serve(lis)
	}()

	cleanup := func() {
		srv.GracefulStop()
		_ = lis.Close()
	}

	return lis.Addr().String(), cleanup
}

func TestNewSyncCmd_Flags(t *testing.T) {
	flags := &RootFlags{}
	cmd := NewSyncCmd(flags)

	if cmd.Use != "sync" {
		t.Errorf("expected command use 'sync', got %q", cmd.Use)
	}

	dryRunFlag := cmd.Flag("dry-run")
	if dryRunFlag == nil {
		t.Fatalf("expected --dry-run flag on sync command")
	}
	if dryRunFlag.DefValue != "false" {
		t.Errorf("expected --dry-run default false, got %s", dryRunFlag.DefValue)
	}
}

func TestNewSyncCmd_MissingWorkspace(t *testing.T) {
	flags := &RootFlags{
		ConfigDir: t.TempDir(),
		Workspace: "non-existent-ws",
	}
	cmd := NewSyncCmd(flags)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected error when syncing non-existent workspace, got nil")
	}
}

func TestSyncCmd_WithControlSocket_Success(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	mgr, err := config.NewManager(configDir)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	// 1. Setup mock control socket server
	sockPath := filepath.Join(tmpDir, "nieblafs.sock")
	ctrlHandler := &mockControlHandler{purged: 17}
	ctrlServer := control.NewServer(sockPath, ctrlHandler)
	if err := ctrlServer.Start(); err != nil {
		t.Fatalf("failed to start control server: %v", err)
	}
	defer ctrlServer.Close()

	// 2. Setup mock gRPC server
	newTreeBytes := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	newTreeHex := hex.EncodeToString(newTreeBytes)
	mockSvc := &mockNieblaSyncService{
		syncResp: &nieblav1.SyncWorkspaceResponse{
			WorkspaceId:    "ws-test",
			ClId:           101,
			ParentCl:       100,
			PatchsetNumber: 2,
			RootTreeHash:   newTreeBytes,
			NeedsRebase:    false,
		},
	}
	serverAddr, cleanupGRPC := startMockGRPCServer(t, mockSvc)
	defer cleanupGRPC()

	// 3. Save workspace config
	wsConfig := &config.WorkspaceConfig{
		WorkspaceID:  "ws-test",
		Name:         "test-ws",
		BaseCL:       101,
		RootTreeHash: "old-tree-hash",
		MountDir:     filepath.Join(tmpDir, "mount"),
		ScratchDir:   filepath.Join(tmpDir, "scratch"),
		SocketPath:   sockPath,
	}
	if err := mgr.Save(wsConfig); err != nil {
		t.Fatalf("failed to save wsConfig: %v", err)
	}

	// 4. Run sync --json
	flags := &RootFlags{
		ConfigDir: configDir,
		Workspace: "test-ws",
		Server:    serverAddr,
		JSON:      true,
	}
	cmd := NewSyncCmd(flags)
	outBuf := new(bytes.Buffer)
	cmd.SetOut(outBuf)
	cmd.SetErr(outBuf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync execution failed: %v\nOutput: %s", err, outBuf.String())
	}

	// 5. Verify control socket received re-anchor
	if !ctrlHandler.reanchorCalled {
		t.Errorf("expected control handler Reanchor to be called, but it was not")
	}
	if ctrlHandler.lastTree != newTreeHex {
		t.Errorf("expected reanchor tree %q, got %q", newTreeHex, ctrlHandler.lastTree)
	}
	if ctrlHandler.lastCL != 101 {
		t.Errorf("expected reanchor CL 101, got %d", ctrlHandler.lastCL)
	}

	// 6. Verify JSON output
	var output SyncOutput
	if err := json.Unmarshal(outBuf.Bytes(), &output); err != nil {
		t.Fatalf("failed to parse sync output JSON: %v, raw: %s", err, outBuf.String())
	}
	if output.ControlPurged != 17 {
		t.Errorf("expected ControlPurged 17, got %d", output.ControlPurged)
	}
	if output.NewRootTreeHash != newTreeHex {
		t.Errorf("expected NewRootTreeHash %s, got %s", newTreeHex, output.NewRootTreeHash)
	}
	if !output.UpToDate {
		t.Errorf("expected UpToDate true")
	}

	// 7. Verify workspace config was updated
	savedWS, err := mgr.Load("test-ws")
	if err != nil {
		t.Fatalf("failed to reload workspace config: %v", err)
	}
	if savedWS.RootTreeHash != newTreeHex {
		t.Errorf("expected saved RootTreeHash %s, got %s", newTreeHex, savedWS.RootTreeHash)
	}
}

func TestSyncCmd_MissingControlSocket_GracefulFallback(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	mgr, err := config.NewManager(configDir)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	newTreeBytes := []byte{0x11, 0x22, 0x33, 0x44}
	newTreeHex := hex.EncodeToString(newTreeBytes)
	mockSvc := &mockNieblaSyncService{
		syncResp: &nieblav1.SyncWorkspaceResponse{
			WorkspaceId:    "ws-fallback",
			ClId:           200,
			PatchsetNumber: 1,
			RootTreeHash:   newTreeBytes,
			NeedsRebase:    false,
		},
	}
	serverAddr, cleanupGRPC := startMockGRPCServer(t, mockSvc)
	defer cleanupGRPC()

	// Non-existent socket path
	wsConfig := &config.WorkspaceConfig{
		WorkspaceID:  "ws-fallback",
		Name:         "fallback-ws",
		BaseCL:       200,
		RootTreeHash: "old-tree",
		MountDir:     filepath.Join(tmpDir, "mount"),
		ScratchDir:   filepath.Join(tmpDir, "scratch"),
		SocketPath:   filepath.Join(tmpDir, "non-existent.sock"),
	}
	if err := mgr.Save(wsConfig); err != nil {
		t.Fatalf("failed to save wsConfig: %v", err)
	}

	flags := &RootFlags{
		ConfigDir: configDir,
		Workspace: "fallback-ws",
		Server:    serverAddr,
		JSON:      true,
	}
	cmd := NewSyncCmd(flags)
	outBuf := new(bytes.Buffer)
	cmd.SetOut(outBuf)
	cmd.SetErr(outBuf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("sync execution should succeed even if control socket is missing: %v", err)
	}

	var output SyncOutput
	if err := json.Unmarshal(outBuf.Bytes(), &output); err != nil {
		t.Fatalf("failed to parse sync output JSON: %v", err)
	}
	if output.ControlPurged != 0 {
		t.Errorf("expected ControlPurged 0 on fallback, got %d", output.ControlPurged)
	}

	savedWS, err := mgr.Load("fallback-ws")
	if err != nil {
		t.Fatalf("failed to reload workspace config: %v", err)
	}
	if savedWS.RootTreeHash != newTreeHex {
		t.Errorf("expected saved RootTreeHash %s, got %s", newTreeHex, savedWS.RootTreeHash)
	}
}

func TestSyncCmd_DryRun(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	mgr, err := config.NewManager(configDir)
	if err != nil {
		t.Fatalf("failed to create config manager: %v", err)
	}

	newTreeBytes := []byte{0x55, 0x66, 0x77}
	mockSvc := &mockNieblaSyncService{
		syncResp: &nieblav1.SyncWorkspaceResponse{
			WorkspaceId:    "ws-dry",
			ClId:           300,
			PatchsetNumber: 1,
			RootTreeHash:   newTreeBytes,
			NeedsRebase:    false,
		},
	}
	serverAddr, cleanupGRPC := startMockGRPCServer(t, mockSvc)
	defer cleanupGRPC()

	wsConfig := &config.WorkspaceConfig{
		WorkspaceID:  "ws-dry",
		Name:         "dry-ws",
		BaseCL:       300,
		RootTreeHash: "old-tree",
		MountDir:     filepath.Join(tmpDir, "mount"),
		ScratchDir:   filepath.Join(tmpDir, "scratch"),
	}
	if err := mgr.Save(wsConfig); err != nil {
		t.Fatalf("failed to save wsConfig: %v", err)
	}

	flags := &RootFlags{
		ConfigDir: configDir,
		Workspace: "dry-ws",
		Server:    serverAddr,
	}
	cmd := NewSyncCmd(flags)
	_ = cmd.Flags().Set("dry-run", "true")
	outBuf := new(bytes.Buffer)
	cmd.SetOut(outBuf)
	cmd.SetErr(outBuf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("dry-run sync failed: %v", err)
	}

	// Workspace config should NOT be updated
	savedWS, _ := mgr.Load("dry-ws")
	if savedWS.RootTreeHash != "old-tree" {
		t.Errorf("expected RootTreeHash to remain 'old-tree' during dry run, got %s", savedWS.RootTreeHash)
	}
}
