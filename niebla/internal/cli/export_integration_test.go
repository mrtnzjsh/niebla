package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportAndNoFuseWorkspace_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	serverAddr, cleanup := setupTestServer(t)
	defer cleanup()

	tmpDir := t.TempDir()
	cfgDir := filepath.Join(tmpDir, "config")
	wsAuthorDir := filepath.Join(tmpDir, "ws-author")

	// 1. Create a --no-fuse workspace
	out, err := runCLI("workspace", "create", "ws-author",
		"--no-fuse",
		"--mount-dir", wsAuthorDir,
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("workspace create --no-fuse failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "NO-FUSE") {
		t.Fatalf("expected NO-FUSE in workspace create output: %s", out)
	}

	// Verify workspace list shows ACTIVE (NO-FUSE)
	listOut, err := runCLI("workspace", "list", "--config-dir", cfgDir)
	if err != nil {
		t.Fatalf("workspace list failed: %v", err)
	}
	if !strings.Contains(listOut, "ACTIVE (NO-FUSE)") {
		t.Fatalf("expected ACTIVE (NO-FUSE) in workspace list output, got:\n%s", listOut)
	}

	// 2. Populate workspace with .nieblaignore, tracked files, and ignored artifacts
	mustWriteFile(t, filepath.Join(wsAuthorDir, ".nieblaignore"), "node_modules/\n*.tmp\n")
	mustWriteFile(t, filepath.Join(wsAuthorDir, "src", "app.go"), "package main\n\nfunc main() {}\n")
	scriptPath := filepath.Join(wsAuthorDir, "bin", "build.sh")
	mustWriteFile(t, scriptPath, "#!/bin/sh\necho ok\n")
	if err := os.Chmod(scriptPath, 0755); err != nil {
		t.Fatalf("Chmod scriptPath: %v", err)
	}

	// Ignored files that must NOT be shelved or exported
	mustWriteFile(t, filepath.Join(wsAuthorDir, "node_modules", "leftpad", "index.js"), "module.exports = {}")
	mustWriteFile(t, filepath.Join(wsAuthorDir, "scratch.tmp"), "temporary data")

	// 3. Verify status excludes ignored files
	statusOut, err := runCLI("status",
		"--workspace", "ws-author",
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("status failed: %v\nOutput: %s", err, statusOut)
	}
	if strings.Contains(statusOut, "node_modules") || strings.Contains(statusOut, "scratch.tmp") {
		t.Fatalf("status unexpectedly included ignored files:\n%s", statusOut)
	}
	if !strings.Contains(statusOut, "src/app.go") || !strings.Contains(statusOut, "bin/build.sh") {
		t.Fatalf("status missing expected tracked files:\n%s", statusOut)
	}

	// 4. Shelve CL
	shelveOut, err := runCLI("shelve",
		"-m", "Add app.go and build.sh with .nieblaignore",
		"--workspace", "ws-author",
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("shelve failed: %v\nOutput: %s", err, shelveOut)
	}

	// 5. Export CL 2 into a clean directory and verify byte-for-byte contents
	exportDir := filepath.Join(tmpDir, "exported-cl2")
	exportOut, err := runCLI("export",
		"--cl", "2",
		"--dest", exportDir,
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("export failed: %v\nOutput: %s", err, exportOut)
	}
	if !strings.Contains(exportOut, "Exported CL 2") {
		t.Fatalf("unexpected export output: %s", exportOut)
	}

	appBytes, err := os.ReadFile(filepath.Join(exportDir, "src", "app.go"))
	if err != nil || string(appBytes) != "package main\n\nfunc main() {}\n" {
		t.Fatalf("exported src/app.go mismatch: %q (err=%v)", string(appBytes), err)
	}

	exportedScript := filepath.Join(exportDir, "bin", "build.sh")
	scriptInfo, err := os.Stat(exportedScript)
	if err != nil {
		t.Fatalf("exported bin/build.sh missing: %v", err)
	}
	if scriptInfo.Mode().Perm() != 0755 {
		t.Errorf("exported bin/build.sh mode = %o, want 0755", scriptInfo.Mode().Perm())
	}

	if _, err := os.Stat(filepath.Join(exportDir, "node_modules")); !os.IsNotExist(err) {
		t.Errorf("expected node_modules to be absent from exported tree")
	}
	if _, err := os.Stat(filepath.Join(exportDir, "scratch.tmp")); !os.IsNotExist(err) {
		t.Errorf("expected scratch.tmp to be absent from exported tree")
	}

	// 6. Submit CL 2 and create a second --no-fuse workspace from trunk head
	if out, err := runCLI("submit", "--workspace", "ws-author", "--server", serverAddr, "--config-dir", cfgDir); err != nil {
		t.Fatalf("submit failed: %v\nOutput: %s", err, out)
	}

	wsReaderDir := filepath.Join(tmpDir, "ws-reader")
	if out, err := runCLI("workspace", "create", "ws-reader",
		"--no-fuse",
		"--mount-dir", wsReaderDir,
		"--server", serverAddr,
		"--config-dir", cfgDir,
	); err != nil {
		t.Fatalf("workspace create ws-reader --no-fuse failed: %v\nOutput: %s", err, out)
	}

	// Verify ws-reader has clean status immediately after materialization
	cleanStatus, err := runCLI("status",
		"--workspace", "ws-reader",
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("status on ws-reader failed: %v", err)
	}
	if strings.TrimSpace(cleanStatus) != "" {
		t.Fatalf("expected clean status on freshly materialized --no-fuse workspace, got:\n%s", cleanStatus)
	}

	// 7. Modify src/app.go and delete bin/build.sh directly on disk in ws-reader
	mustWriteFile(t, filepath.Join(wsReaderDir, "src", "app.go"), "package main\n\nfunc main() { println(42) }\n")
	if err := os.Remove(filepath.Join(wsReaderDir, "bin", "build.sh")); err != nil {
		t.Fatalf("remove build.sh: %v", err)
	}

	dirtyStatus, err := runCLI("status",
		"--workspace", "ws-reader",
		"--server", serverAddr,
		"--config-dir", cfgDir,
	)
	if err != nil {
		t.Fatalf("status after edit/delete failed: %v", err)
	}
	if !strings.Contains(dirtyStatus, "D  bin/build.sh") || !strings.Contains(dirtyStatus, "M  src/app.go") {
		t.Fatalf("expected D bin/build.sh and M src/app.go in --no-fuse status, got:\n%s", dirtyStatus)
	}
}
