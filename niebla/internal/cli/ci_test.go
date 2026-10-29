package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"niebla/pkg/ci"
)

func TestCIReportCLI(t *testing.T) {
	var receivedReport ci.CheckRunReport
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedReport)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// 1. Missing required flags
	cmd := NewRootCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"ci", "report"})
	if err := cmd.Execute(); err == nil {
		t.Fatalf("expected error for missing required flags")
	}

	// 2. Successful report with --json
	outBuf.Reset()
	cmd = NewRootCmd()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{
		"--json",
		"ci", "report",
		"--server-url", ts.URL,
		"--cl", "12",
		"--patchset", "3",
		"--runner", "unit-tests",
		"--status", "passed",
		"--url", "https://ci.example.com/build/123",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("ci report failed: %v", err)
	}

	var jsonOutput map[string]any
	if err := json.Unmarshal(outBuf.Bytes(), &jsonOutput); err != nil {
		t.Fatalf("failed to decode JSON output: %v, body: %s", err, outBuf.String())
	}
	if jsonOutput["command"] != "ci report" || jsonOutput["status"] != "ok" {
		t.Errorf("unexpected JSON output: %+v", jsonOutput)
	}

	if receivedReport.CLID != 12 || receivedReport.PatchsetNumber != 3 || receivedReport.Status != "passed" {
		t.Errorf("unexpected received report: %+v", receivedReport)
	}
}

func TestCIExecCLI_Validation(t *testing.T) {
	cmd := NewRootCmd()
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)

	// Missing --cl
	cmd.SetArgs([]string{"ci", "exec", "--runner", "test", "--", "echo", "hi"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--cl is required") {
		t.Errorf("expected error for missing --cl, got: %v", err)
	}

	// Missing runner
	outBuf.Reset()
	cmd = NewRootCmd()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"ci", "exec", "--cl", "1", "--", "echo", "hi"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--runner is required") {
		t.Errorf("expected error for missing --runner, got: %v", err)
	}

	// Missing command after --
	outBuf.Reset()
	cmd = NewRootCmd()
	cmd.SetOut(&outBuf)
	cmd.SetErr(&outBuf)
	cmd.SetArgs([]string{"ci", "exec", "--cl", "1", "--runner", "test"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "no command provided") {
		t.Errorf("expected error for missing command, got: %v", err)
	}
}
