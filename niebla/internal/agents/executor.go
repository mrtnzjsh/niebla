package agents

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"niebla/internal/db"
	"niebla/pkg/agents"
	"niebla/pkg/cas"
)

// Executor runs review agent personas against a materialized changelist workspace.
type Executor struct {
	db         *db.DB
	casStorage *cas.Storage
	toolsDir   string
}

// NewExecutor constructs a new review agent executor.
func NewExecutor(database *db.DB, casStorage *cas.Storage, toolsDir string) *Executor {
	if toolsDir == "" {
		home, _ := os.UserHomeDir()
		toolsDir = filepath.Join(home, ".cache", "niebla", "tools")
	}
	_ = os.MkdirAll(toolsDir, 0755)
	return &Executor{
		db:         database,
		casStorage: casStorage,
		toolsDir:   toolsDir,
	}
}

// ExecutionRequest specifies the parameters to run a review agent persona.
type ExecutionRequest struct {
	CLID         int64
	Patchset     int
	Agent        agents.AgentDef
	WorkspaceDir string
}

// ExecutionResult captures the output of a review agent run.
type ExecutionResult struct {
	Passed      bool
	LogBlobSHA  []byte
	Diagnostics []agents.DiagnosticComment
	Output      []byte
	Error       error
}

// Execute runs the agent, uploads logs to CAS, creates comments, and records approvals.
func (e *Executor) Execute(ctx context.Context, req ExecutionRequest) (*ExecutionResult, error) {
	timeout := 60 * time.Second
	if req.Agent.TimeoutSeconds > 0 {
		timeout = time.Duration(req.Agent.TimeoutSeconds) * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if req.Patchset <= 0 {
		req.Patchset = 1
	}

	// 1. Mark check run as running in PostgreSQL
	_, _ = e.db.UpsertCheckRun(execCtx, db.CheckRun{
		CLID:           req.CLID,
		PatchsetNumber: req.Patchset,
		RunnerName:     req.Agent.Name,
		Provider:       "agent",
		Status:         "running",
	})

	// 2. Prepare command
	var cmd *exec.Cmd
	switch req.Agent.Type {
	case "in_tree":
		entryPath := filepath.Join(req.WorkspaceDir, req.Agent.Entrypoint)
		info, err := os.Stat(entryPath)
		if err != nil {
			return nil, fmt.Errorf("in_tree entrypoint %s not found: %w", req.Agent.Entrypoint, err)
		}
		if info.Mode().Perm()&0111 == 0 {
			_ = os.Chmod(entryPath, 0755)
		}
		cmd = exec.CommandContext(execCtx, entryPath, req.Agent.Args...)

	case "cas_binary":
		binaryPath, err := e.ensureCASBinary(req.Agent.CasSHA256)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare CAS binary %s: %w", req.Agent.CasSHA256, err)
		}
		cmd = exec.CommandContext(execCtx, binaryPath, req.Agent.Args...)

	case "exec":
		cmd = exec.CommandContext(execCtx, "sh", "-c", req.Agent.Command)

	default:
		// Default to shell exec if command provided
		if req.Agent.Command != "" {
			cmd = exec.CommandContext(execCtx, "sh", "-c", req.Agent.Command)
		} else {
			return nil, fmt.Errorf("unsupported agent type %q without command", req.Agent.Type)
		}
	}

	cmd.Dir = req.WorkspaceDir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("NIEBLA_CL_ID=%d", req.CLID),
		fmt.Sprintf("NIEBLA_PATCHSET=%d", req.Patchset),
		fmt.Sprintf("NIEBLA_WORKSPACE=%s", req.WorkspaceDir),
	)

	// 3. Execute
	output, runErr := cmd.CombinedOutput()
	passed := (runErr == nil)

	res := &ExecutionResult{
		Passed: passed,
		Output: output,
		Error:  runErr,
	}

	// 4. Ingest logs into CAS if enabled
	var logSHA []byte
	if req.Agent.CaptureLogs && len(output) > 0 {
		hashBytes, _, size, storagePath, pErr := e.casStorage.Put(bytes.NewReader(output))
		if pErr == nil {
			logSHA = hashBytes
			_ = e.db.InsertBlob(execCtx, hashBytes, size, storagePath)
			res.LogBlobSHA = hashBytes
		}
	}

	// 5. Update check run terminal state
	runStatus := "passed"
	if !passed {
		runStatus = "failed"
	}
	var logURL string
	if len(logSHA) > 0 {
		logURL = fmt.Sprintf("/cl/%d/runs/%s/logs", req.CLID, req.Agent.Name)
	}

	_, _ = e.db.UpsertCheckRun(execCtx, db.CheckRun{
		CLID:           req.CLID,
		PatchsetNumber: req.Patchset,
		RunnerName:     req.Agent.Name,
		Provider:       "agent",
		Status:         runStatus,
		ExternalURL:    logURL,
		LogBlobSHA256:  logSHA,
	})

	// 6. Post inline diagnostics to Cotejo if failed
	if !passed && req.Agent.PostInlineDiagnostics && len(output) > 0 {
		diags := agents.ParseDiagnostics(string(output))
		res.Diagnostics = diags

		displayName := req.Agent.DisplayName
		if displayName == "" {
			displayName = req.Agent.Name
		}

		for _, diag := range diags {
			body := fmt.Sprintf("⚠️ **%s** (%s):\n```\n%s\n```", displayName, strings.ToUpper(diag.Severity), diag.Message)
			if logURL != "" {
				body += fmt.Sprintf("\n\n[View Full Build Log](%s)", logURL)
			}
			_, _ = e.db.CreateComment(
				execCtx,
				req.CLID,
				req.Patchset,
				diag.Path,
				diag.LineStart,
				diag.LineEnd,
				"modified",
				req.Agent.Name,
				body,
				nil,
				false, // is_draft = false (published immediately)
			)
		}
	}

	// 7. Cast approval verdict
	if passed && req.Agent.AutoApprove {
		_ = e.db.SetApproval(execCtx, req.CLID, req.Agent.Name, "lgtm")
	} else if !passed {
		_ = e.db.SetApproval(execCtx, req.CLID, req.Agent.Name, "changes_requested")
	}

	return res, nil
}

// ensureCASBinary checks local cache or fetches the binary from CAS storage.
func (e *Executor) ensureCASBinary(shaHex string) (string, error) {
	shaBytes, err := hex.DecodeString(strings.TrimSpace(shaHex))
	if err != nil || len(shaBytes) != 32 {
		return "", fmt.Errorf("invalid sha256 hex string: %s", shaHex)
	}

	destPath := filepath.Join(e.toolsDir, shaHex)
	if _, err := os.Stat(destPath); err == nil {
		return destPath, nil
	}

	f, _, err := e.casStorage.OpenBlob(shaBytes)
	if err != nil {
		return "", fmt.Errorf("blob %s not found in CAS storage: %w", shaHex, err)
	}
	defer f.Close()

	tmpPath := destPath + ".tmp"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create temp binary %s: %w", tmpPath, err)
	}

	if _, err := io.Copy(out, f); err != nil {
		out.Close()
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to stream blob: %w", err)
	}
	out.Close()

	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", fmt.Errorf("failed to rename cached binary: %w", err)
	}

	return destPath, nil
}
