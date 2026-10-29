package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testHandler struct {
	pingCalled      bool
	reanchorCalled  bool
	invalidatePaths []string
	rootTreeHash    string
	clID            int64
	purgedCount     int
}

func (h *testHandler) HandlePing(ctx context.Context) error {
	h.pingCalled = true
	return nil
}

func (h *testHandler) HandleReanchor(ctx context.Context, newRootTreeHash string, clID int64) (int, error) {
	h.reanchorCalled = true
	h.rootTreeHash = newRootTreeHash
	h.clID = clID
	return h.purgedCount, nil
}

func (h *testHandler) HandleInvalidate(ctx context.Context, paths []string) (int, error) {
	h.invalidatePaths = paths
	return len(paths), nil
}

func (h *testHandler) HandleStatus(ctx context.Context) (Response, error) {
	return Response{
		Status:        "ok",
		RootTreeHash:  h.rootTreeHash,
		CLID:          h.clID,
		CachedEntries: 42,
	}, nil
}

func TestControlSocket_Lifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test-nieblafs.sock")

	handler := &testHandler{
		rootTreeHash: "initial-tree-hash",
		clID:         1,
		purgedCount:  5,
	}

	server := NewServer(sockPath, handler)
	if err := server.Start(); err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	defer server.Close()

	// Wait briefly for socket to become ready
	client := NewClient(sockPath)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// 1. Ping
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("client.Ping: %v", err)
	}
	if !handler.pingCalled {
		t.Fatalf("expected handler.HandlePing to be called")
	}

	// 2. Reanchor
	resp, err := client.Reanchor(ctx, "new-reanchor-hash", 2)
	if err != nil {
		t.Fatalf("client.Reanchor: %v", err)
	}
	if resp.Status != "ok" || resp.PurgedEntries != 5 || resp.RootTreeHash != "new-reanchor-hash" {
		t.Fatalf("unexpected reanchor response: %+v", resp)
	}
	if !handler.reanchorCalled || handler.rootTreeHash != "new-reanchor-hash" || handler.clID != 2 {
		t.Fatalf("unexpected handler state: %+v", handler)
	}

	// 3. Invalidate
	invalResp, err := client.Invalidate(ctx, []string{"foo/bar.go", "baz.txt"})
	if err != nil {
		t.Fatalf("client.Invalidate: %v", err)
	}
	if invalResp.PurgedEntries != 2 {
		t.Fatalf("expected 2 purged entries, got %d", invalResp.PurgedEntries)
	}
	if len(handler.invalidatePaths) != 2 {
		t.Fatalf("expected 2 paths passed to handler, got %v", handler.invalidatePaths)
	}

	// 4. Status
	statusResp, err := client.Status(ctx)
	if err != nil {
		t.Fatalf("client.Status: %v", err)
	}
	if statusResp.Status != "ok" || statusResp.CachedEntries != 42 || statusResp.RootTreeHash != "new-reanchor-hash" {
		t.Fatalf("unexpected status response: %+v", statusResp)
	}
	if statusResp.UptimeSeconds <= 0 {
		t.Errorf("expected positive uptime, got %f", statusResp.UptimeSeconds)
	}

	// 5. Close server and verify socket removed
	if err := server.Close(); err != nil {
		t.Fatalf("server.Close: %v", err)
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Fatalf("expected socket file to be deleted upon close, err: %v", err)
	}

	// 6. Connect to closed socket -> ErrSocketNotFound
	if err := client.Ping(ctx); err == nil {
		t.Fatalf("expected error connecting to closed socket")
	}
}

func TestControlSocket_InvalidAction(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "invalid-nieblafs.sock")

	handler := &testHandler{}
	server := NewServer(sockPath, handler)
	if err := server.Start(); err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	defer server.Close()

	client := NewClient(sockPath)
	ctx := context.Background()

	_, err := client.send(ctx, Request{Action: "bogus_command"})
	if err == nil {
		t.Fatalf("expected error for bogus action, got nil")
	}
	expectedMsg := "unknown control action \"bogus_command\""
	if err.Error() != fmt.Sprintf("nieblafs control error: %s", expectedMsg) {
		t.Errorf("unexpected error message: %v", err)
	}
}
