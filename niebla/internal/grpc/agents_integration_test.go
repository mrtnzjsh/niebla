package grpc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalagents "niebla/internal/agents"
	pkgagents "niebla/pkg/agents"
	nieblav1 "niebla/pkg/proto/nieblav1"
)

func TestAgentsIntegration_FullReviewAgentLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Establish base trunk CL with .niebla/agents.toml and tool scripts
	baseTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
		".niebla/agents.toml": `
[[agent]]
name = "sec-scanner"
display_name = "Security Pattern Scanner"
type = "in_tree"
entrypoint = "tools/agents/sec-scanner.sh"
trigger_paths = ["pkg/security/**"]
auto_approve = true
capture_logs = true
post_inline_diagnostics = true

[[agent]]
name = "package-builder"
display_name = "Package Build Runner"
type = "in_tree"
entrypoint = "tools/agents/builder.sh"
trigger_paths = ["src/**"]
auto_approve = true
capture_logs = true
`,
		"tools/agents/sec-scanner.sh": `#!/bin/sh
# Default passing scanner
echo "all security checks passed"
exit 0
`,
		"tools/agents/builder.sh": `#!/bin/sh
echo "build passed"
exit 0
`,
		"README.md": "# Base Monorepo\n",
	})

	baseResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "admin@corp.internal",
		Description:  "feat: initial monorepo setup",
		RootTreeHash: baseTree,
	})
	if err != nil {
		t.Fatalf("failed to shelve base CL: %v", err)
	}

	// Submit base CL to establish trunk
	_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: baseResp.ClId, Force: true})
	if err != nil {
		t.Fatalf("failed to submit base CL: %v", err)
	}
	headCLID := baseResp.ClId

	// 2. Shelve CL with security vulnerability (triggers sec-scanner failure)
	failingScript := `#!/bin/sh
echo "pkg/security/auth.go:15: error: hardcoded secret detected"
exit 1
`
	cl1Tree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
		".niebla/agents.toml": `
[[agent]]
name = "sec-scanner"
display_name = "Security Pattern Scanner"
type = "in_tree"
entrypoint = "tools/agents/sec-scanner.sh"
trigger_paths = ["pkg/security/**"]
auto_approve = true
capture_logs = true
post_inline_diagnostics = true
`,
		"tools/agents/sec-scanner.sh": failingScript,
		"pkg/security/auth.go":        "package security\nconst Secret = \"12345\"\n",
	})

	cl1Resp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "dev@corp.internal",
		Description:  "feat: add security credentials",
		ParentCl:     headCLID,
		RootTreeHash: cl1Tree,
	})
	if err != nil {
		t.Fatalf("failed to shelve CL 1: %v", err)
	}
	clID := cl1Resp.ClId

	// Verify sec-scanner is auto-assigned to reviewers
	reviewers, err := database.ListReviewers(ctx, clID)
	if err != nil {
		t.Fatalf("failed to list reviewers: %v", err)
	}
	hasSecScanner := false
	for _, r := range reviewers {
		if r.Reviewer == "sec-scanner" {
			hasSecScanner = true
			break
		}
	}
	if !hasSecScanner {
		t.Fatalf("expected 'sec-scanner' to be auto-assigned to reviewers")
	}

	// 3. Execute Review Agent against materialized workspace
	tempDir := t.TempDir()
	wsDir := filepath.Join(tempDir, "workspace")
	toolsDir := filepath.Join(tempDir, "tools")
	agentScriptDir := filepath.Join(wsDir, "tools", "agents")
	_ = os.MkdirAll(agentScriptDir, 0755)

	// Materialize script into workspace
	if err := os.WriteFile(filepath.Join(agentScriptDir, "sec-scanner.sh"), []byte(failingScript), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	executor := internalagents.NewExecutor(database, casStorage, toolsDir)
	secAgentDef := pkgagents.AgentDef{
		Name:                  "sec-scanner",
		DisplayName:           "Security Pattern Scanner",
		Type:                  "in_tree",
		Entrypoint:            "tools/agents/sec-scanner.sh",
		AutoApprove:           true,
		CaptureLogs:           true,
		PostInlineDiagnostics: true,
	}

	execRes, err := executor.Execute(ctx, internalagents.ExecutionRequest{
		CLID:         clID,
		Patchset:     1,
		Agent:        secAgentDef,
		WorkspaceDir: wsDir,
	})
	if err != nil {
		t.Fatalf("agent execution failed: %v", err)
	}

	if execRes.Passed {
		t.Fatalf("expected scanner to fail, but passed")
	}
	if len(execRes.Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(execRes.Diagnostics))
	}

	// 4. Verify Cotejo integration state
	// a. Check run recorded as 'failed' with CAS log blob
	runs, err := database.ListCheckRunsForPatchset(ctx, clID, 1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("expected 1 check run, got %d (err: %v)", len(runs), err)
	}
	if runs[0].Status != "failed" || len(runs[0].LogBlobSHA256) == 0 {
		t.Errorf("unexpected check run state: %+v", runs[0])
	}

	// b. Inline comment created on pkg/security/auth.go:15
	comments, err := database.ListAllComments(ctx, clID)
	if err != nil || len(comments) != 1 {
		t.Fatalf("expected 1 inline comment, got %d (err: %v)", len(comments), err)
	}
	if comments[0].FilePath != "pkg/security/auth.go" || comments[0].LineNumber != 15 || comments[0].Author != "sec-scanner" {
		t.Errorf("unexpected comment created: %+v", comments[0])
	}
	if !strings.Contains(comments[0].Body, "hardcoded secret detected") {
		t.Errorf("comment body missing diagnostic message: %s", comments[0].Body)
	}

	// c. Reviewer approval set to 'changes_requested'
	approvals, err := database.ListApprovals(ctx, clID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("expected 1 approval, got %d (err: %v)", len(approvals), err)
	}
	if approvals[0].Reviewer != "sec-scanner" || approvals[0].Verdict != "changes_requested" {
		t.Errorf("expected changes_requested, got: %+v", approvals[0])
	}

	// 5. Fix vulnerability in Patchset 2 -> re-run agent -> auto-approves
	passingScript := `#!/bin/sh
echo "all security checks passed"
exit 0
`
	if err := os.WriteFile(filepath.Join(agentScriptDir, "sec-scanner.sh"), []byte(passingScript), 0755); err != nil {
		t.Fatalf("failed to write passing script: %v", err)
	}

	passRes, err := executor.Execute(ctx, internalagents.ExecutionRequest{
		CLID:         clID,
		Patchset:     2,
		Agent:        secAgentDef,
		WorkspaceDir: wsDir,
	})
	if err != nil {
		t.Fatalf("agent re-execution failed: %v", err)
	}
	if !passRes.Passed {
		t.Fatalf("expected fixed script to pass")
	}

	// Verify approval updated to 'lgtm'
	approvals2, err := database.ListApprovals(ctx, clID)
	if err != nil || len(approvals2) != 1 {
		t.Fatalf("expected 1 approval, got %d", len(approvals2))
	}
	if approvals2[0].Reviewer != "sec-scanner" || approvals2[0].Verdict != "lgtm" {
		t.Errorf("expected lgtm verdict after fix, got: %+v", approvals2[0])
	}
}
