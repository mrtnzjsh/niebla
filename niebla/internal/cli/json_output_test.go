package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"niebla/internal/cli/config"
)

func runCLIStdout(args ...string) (string, string, error) {
	rootCmd := NewRootCmd()
	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	rootCmd.SetOut(&outBuf)
	rootCmd.SetErr(&errBuf)
	rootCmd.SetArgs(args)

	err := rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestCLIJSONOutputContracts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	serverAddr, cleanup := setupTestServer(t)
	defer cleanup()

	tmpDir := t.TempDir()
	cfgDir := filepath.Join(tmpDir, "config")
	wsDir := filepath.Join(tmpDir, "ws-json")

	// 1. workspace create --json
	stdout, stderr, err := runCLIStdout("workspace", "create", "ws-json",
		"--no-fuse",
		"--mount-dir", wsDir,
		"--server", serverAddr,
		"--config-dir", cfgDir,
		"--json",
	)
	if err != nil {
		t.Fatalf("workspace create --json failed: %v (stderr: %s)", err, stderr)
	}
	var createdWS config.WorkspaceConfig
	if err := json.Unmarshal([]byte(stdout), &createdWS); err != nil {
		t.Fatalf("failed to parse workspace create JSON: %v\nstdout: %s", err, stdout)
	}
	if createdWS.Name != "ws-json" || !createdWS.NoFuse || createdWS.BaseCL != 1 {
		t.Errorf("unexpected workspace create JSON payload: %+v", createdWS)
	}

	// 2. workspace list --json
	stdout, stderr, err = runCLIStdout("workspace", "list",
		"--config-dir", cfgDir,
		"--json",
	)
	if err != nil {
		t.Fatalf("workspace list --json failed: %v (stderr: %s)", err, stderr)
	}
	var listed []workspaceListEntry
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("failed to parse workspace list JSON: %v\nstdout: %s", err, stdout)
	}
	if len(listed) != 1 || listed[0].Name != "ws-json" || listed[0].Status != "ACTIVE (NO-FUSE)" {
		t.Errorf("unexpected workspace list JSON payload: %+v", listed)
	}

	// 3. status --json (with 1 added file)
	mustWriteFile(t, filepath.Join(wsDir, "agent.go"), "package agent\n")
	stdout, stderr, err = runCLIStdout("status",
		"--workspace", "ws-json",
		"--server", serverAddr,
		"--config-dir", cfgDir,
		"--json",
	)
	if err != nil {
		t.Fatalf("status --json failed: %v (stderr: %s)", err, stderr)
	}
	var statusPayload StatusOutput
	if err := json.Unmarshal([]byte(stdout), &statusPayload); err != nil {
		t.Fatalf("failed to parse status JSON: %v\nstdout: %s", err, stdout)
	}
	if statusPayload.Clean || len(statusPayload.Changes) != 1 || statusPayload.Changes[0].Path != "agent.go" || statusPayload.Changes[0].Type != ChangeAdded {
		t.Errorf("unexpected status JSON payload: %+v", statusPayload)
	}

	// 4. shelve --json
	stdout, stderr, err = runCLIStdout("shelve",
		"-m", "Agent automated change",
		"--workspace", "ws-json",
		"--server", serverAddr,
		"--config-dir", cfgDir,
		"--json",
	)
	if err != nil {
		t.Fatalf("shelve --json failed: %v (stderr: %s)", err, stderr)
	}
	var shelvePayload ShelveOutput
	if err := json.Unmarshal([]byte(stdout), &shelvePayload); err != nil {
		t.Fatalf("failed to parse shelve JSON: %v\nstdout: %s", err, stdout)
	}
	if shelvePayload.CLID <= 1 || shelvePayload.FilesShelved != 1 || shelvePayload.Description != "Agent automated change" {
		t.Errorf("unexpected shelve JSON payload: %+v", shelvePayload)
	}

	// 5. sync --json
	stdout, stderr, err = runCLIStdout("sync",
		"--workspace", "ws-json",
		"--server", serverAddr,
		"--config-dir", cfgDir,
		"--json",
	)
	if err != nil {
		t.Fatalf("sync --json failed: %v (stderr: %s)", err, stderr)
	}
	var syncPayload SyncOutput
	if err := json.Unmarshal([]byte(stdout), &syncPayload); err != nil {
		t.Fatalf("failed to parse sync JSON: %v\nstdout: %s", err, stdout)
	}
	if !syncPayload.UpToDate || syncPayload.NeedsRebase || syncPayload.CLID != shelvePayload.CLID {
		t.Errorf("unexpected sync JSON payload: %+v", syncPayload)
	}

	// 6. export --json
	exportDir := filepath.Join(tmpDir, "export-json")
	stdout, stderr, err = runCLIStdout("export",
		"--cl", "2",
		"--dest", exportDir,
		"--server", serverAddr,
		"--config-dir", cfgDir,
		"--json",
	)
	if err != nil {
		t.Fatalf("export --json failed: %v (stderr: %s)", err, stderr)
	}
	var exportPayload ExportOutput
	if err := json.Unmarshal([]byte(stdout), &exportPayload); err != nil {
		t.Fatalf("failed to parse export JSON: %v\nstdout: %s", err, stdout)
	}
	if exportPayload.CLID != shelvePayload.CLID || exportPayload.FilesMaterialized != 1 {
		t.Errorf("unexpected export JSON payload: %+v", exportPayload)
	}

	// 7. submit --json
	stdout, stderr, err = runCLIStdout("submit",
		"--workspace", "ws-json",
		"--server", serverAddr,
		"--config-dir", cfgDir,
		"--json",
	)
	if err != nil {
		t.Fatalf("submit --json failed: %v (stderr: %s)", err, stderr)
	}
	var submitPayload SubmitOutput
	if err := json.Unmarshal([]byte(stdout), &submitPayload); err != nil {
		t.Fatalf("failed to parse submit JSON: %v\nstdout: %s", err, stdout)
	}
	if submitPayload.CLID != shelvePayload.CLID || submitPayload.Status != "submitted" || submitPayload.CommittedAtUnix <= 0 {
		t.Errorf("unexpected submit JSON payload: %+v", submitPayload)
	}
}
