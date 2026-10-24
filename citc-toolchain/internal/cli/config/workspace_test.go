package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceConfigCRUD(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "citc-cfg-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	// 1. Initial list should be empty
	list, err := mgr.List()
	if err != nil {
		t.Fatalf("List() failed: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected 0 workspaces, got %d", len(list))
	}

	// 2. Save a workspace
	ws := &WorkspaceConfig{
		WorkspaceID:  "ws-uuid-1",
		Name:         "test-ws",
		BaseCL:       10001,
		RootTreeHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		MountDir:     "/home/user/citc/test-ws",
		ScratchDir:   "/citc/workspaces/test-ws/scratch",
		PID:          12345,
	}

	if err := mgr.Save(ws); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// 3. Load workspace
	loaded, err := mgr.Load("test-ws")
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
	if loaded.WorkspaceID != ws.WorkspaceID || loaded.Name != ws.Name || loaded.BaseCL != ws.BaseCL {
		t.Fatalf("loaded config does not match saved: %+v", loaded)
	}

	// 4. Update workspace (e.g. active CL)
	loaded.ActiveCL = 10042
	if err := mgr.Save(loaded); err != nil {
		t.Fatalf("Save update failed: %v", err)
	}
	updated, err := mgr.Load("test-ws")
	if err != nil {
		t.Fatalf("Load after update failed: %v", err)
	}
	if updated.ActiveCL != 10042 {
		t.Fatalf("expected ActiveCL 10042, got %d", updated.ActiveCL)
	}

	// 5. List should return test-ws
	list, err = mgr.List()
	if err != nil {
		t.Fatalf("List() after save failed: %v", err)
	}
	if len(list) != 1 || list[0].Name != "test-ws" {
		t.Fatalf("unexpected list result: %+v", list)
	}

	// 6. Delete
	if err := mgr.Delete("test-ws"); err != nil {
		t.Fatalf("Delete() failed: %v", err)
	}
	_, err = mgr.Load("test-ws")
	if err == nil {
		t.Fatalf("expected error loading deleted workspace")
	}
}

func TestResolveWorkspace(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "citc-cfg-resolve-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	mgr, err := NewManager(tempDir)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	mountRoot := filepath.Join(tempDir, "mounts", "ws1")
	if err := os.MkdirAll(mountRoot, 0755); err != nil {
		t.Fatalf("failed to create mount root: %v", err)
	}

	ws := &WorkspaceConfig{
		WorkspaceID:  "ws-uuid-1",
		Name:         "ws1",
		BaseCL:       100,
		RootTreeHash: "hash1",
		MountDir:     mountRoot,
		ScratchDir:   filepath.Join(tempDir, "scratch"),
		PID:          9999,
	}
	if err := mgr.Save(ws); err != nil {
		t.Fatalf("failed to save ws: %v", err)
	}

	// 1. Resolve explicitly by name
	resolved, err := mgr.ResolveWorkspace("ws1", "/some/other/path")
	if err != nil || resolved.Name != "ws1" {
		t.Fatalf("explicit resolution failed: %v, ws=%+v", err, resolved)
	}

	// 2. Resolve implicitly by exact mount dir
	resolved, err = mgr.ResolveWorkspace("", mountRoot)
	if err != nil || resolved.Name != "ws1" {
		t.Fatalf("implicit exact resolution failed: %v, ws=%+v", err, resolved)
	}

	// 3. Resolve implicitly by subdirectory inside mount dir
	subDir := filepath.Join(mountRoot, "subdir", "deep")
	_ = os.MkdirAll(subDir, 0755)
	resolved, err = mgr.ResolveWorkspace("", subDir)
	if err != nil || resolved.Name != "ws1" {
		t.Fatalf("implicit subdir resolution failed: %v, ws=%+v", err, resolved)
	}

	// 4. Resolve outside mount dir should fail
	_, err = mgr.ResolveWorkspace("", "/tmp")
	if err == nil {
		t.Fatalf("expected error when outside mount dir")
	}
}
