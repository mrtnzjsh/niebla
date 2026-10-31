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

	"niebla/internal/config"
	"niebla/internal/db"
	"niebla/pkg/agents"
	"niebla/pkg/ai"
	"niebla/pkg/cas"
	"niebla/pkg/httpclient"
)

// Executor runs review agent personas against a materialized changelist workspace.
type Executor struct {
	db         *db.DB
	casStorage *cas.Storage
	toolsDir   string
	aiConfig   *config.CritiqueAIConfig
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

// WithAIConfig attaches an OpenAI-compatible AI configuration to the executor for "ai" persona agents.
func (e *Executor) WithAIConfig(aiCfg *config.CritiqueAIConfig) *Executor {
	e.aiConfig = aiCfg
	return e
}

// ExecutionRequest specifies the parameters to run a review agent persona.
type ExecutionRequest struct {
	CLID         int64
	Patchset     int
	Agent        agents.AgentDef
	WorkspaceDir string
	TouchedFiles []string
}

// ExecutionResult captures the output of a review agent run.
type ExecutionResult struct {
	Passed      bool
	Skipped     bool
	LogBlobSHA  []byte
	Report      *agents.AgentReviewReport
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

	// 2. Execute agent (either built-in AI persona or external process)
	var output []byte
	var runErr error
	var passed bool

	if req.Agent.Type == "ai" {
		if e.aiConfig == nil || !e.aiConfig.IsConfigured() {
			_, _ = e.db.UpsertCheckRun(execCtx, db.CheckRun{
				CLID:           req.CLID,
				PatchsetNumber: req.Patchset,
				RunnerName:     req.Agent.Name,
				Provider:       "agent",
				Status:         "skipped",
			})
			return &ExecutionResult{
				Passed:  true,
				Skipped: true,
			}, nil
		}
		output, runErr = e.executeAIPersona(execCtx, req)
		passed = (runErr == nil)
	} else {
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
			fmt.Sprintf("NIEBLA_TOUCHED_FILES=%s", strings.Join(req.TouchedFiles, ",")),
		)

		output, runErr = cmd.CombinedOutput()
		passed = (runErr == nil)
	}

	// Parse output for structured JSON report OR compiler diagnostic lines
	report, diags := agents.ParseReviewOutput(string(output))
	if report != nil {
		switch strings.ToLower(strings.TrimSpace(report.Verdict)) {
		case "changes_requested":
			passed = false
		case "lgtm", "approved":
			if runErr == nil {
				passed = true
			}
		}
	}

	res := &ExecutionResult{
		Passed:      passed,
		Report:      report,
		Diagnostics: diags,
		Output:      output,
		Error:       runErr,
	}

	// 4. Ingest logs into CAS if enabled
	var logSHA []byte
	if req.Agent.CaptureLogs && len(output) > 0 && e.casStorage != nil {
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

	// 6. Post inline diagnostics / structured suggestions to Cotejo
	shouldPostComments := (req.Agent.PostInlineDiagnostics || report != nil) && len(diags) > 0 && (!passed || report != nil)
	if shouldPostComments {
		displayName := req.Agent.DisplayName
		if displayName == "" {
			displayName = req.Agent.Name
		}

		for _, diag := range diags {
			// Avoid posting duplicate comments if the same agent runs more than once on the same patchset
			if existing, lErr := e.db.ListComments(execCtx, req.CLID, diag.Path); lErr == nil {
				alreadyPosted := false
				for _, ec := range existing {
					if ec.PatchsetNumber == req.Patchset && ec.LineNumber == diag.LineStart && strings.EqualFold(ec.Author, req.Agent.Name) {
						alreadyPosted = true
						break
					}
				}
				if alreadyPosted {
					continue
				}
			}

			var body string
			if report != nil && diag.SuggestionCode != "" {
				body = fmt.Sprintf("🤖 **%s** (%s):\n%s", displayName, strings.ToUpper(diag.Severity), diag.Message)
			} else {
				body = fmt.Sprintf("⚠️ **%s** (%s):\n```\n%s\n```", displayName, strings.ToUpper(diag.Severity), diag.Message)
			}
			if logURL != "" {
				body += fmt.Sprintf("\n\n[View Full Build Log](%s)", logURL)
			}

			if strings.TrimSpace(diag.SuggestionCode) != "" {
				_, _ = e.db.CreateCommentWithSuggestion(
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
					false, // published immediately
					diag.SuggestionStartLine,
					diag.SuggestionEndLine,
					diag.SuggestionCode,
				)
			} else {
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
					false, // published immediately
				)
			}
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

// DefaultAIReviewSystemPrompt instructs an AI review persona to emit an AgentReviewReport JSON.
const DefaultAIReviewSystemPrompt = `You are an automated specialized code review agent for the Niebla version control system.
Review the provided files and return ONLY a valid JSON object matching this schema:
{
  "verdict": "lgtm" | "changes_requested",
  "summary": "1-sentence summary of findings",
  "comments": [
    {
      "path": "relative/path/to/file",
      "line_start": <1-based start line>,
      "line_end": <1-based end line>,
      "severity": "error" | "warning" | "info",
      "message": "Clear explanation of the issue and why the fix improves it",
      "suggestion_start_line": <1-based start line>,
      "suggestion_end_line": <1-based end line>,
      "suggestion_code": "<exact replacement code for lines [suggestion_start_line, suggestion_end_line] without markdown fences>"
    }
  ]
}
If no issues are found, return {"verdict": "lgtm", "summary": "No issues detected.", "comments": []}.`

func (e *Executor) executeAIPersona(ctx context.Context, req ExecutionRequest) ([]byte, error) {
	aiClient, err := ai.NewClient(ai.Config{
		Endpoint:       e.aiConfig.Endpoint,
		APIKey:         e.aiConfig.APIKey,
		APIKeyEnv:      e.aiConfig.APIKeyEnv,
		Model:          e.aiConfig.Model,
		TimeoutSeconds: e.aiConfig.TimeoutSeconds,
		Temperature:    e.aiConfig.Temperature,
		TLS: httpclient.TLSOptions{
			Enabled:    e.aiConfig.TLS.Enabled,
			CAFile:     e.aiConfig.TLS.CAFile,
			ServerName: e.aiConfig.TLS.ServerName,
		},
	})
	if err != nil {
		return nil, err
	}

	sysPrompt := DefaultAIReviewSystemPrompt
	promptPath := strings.TrimSpace(req.Agent.PromptFile)
	if promptPath == "" {
		promptPath = strings.TrimSpace(e.aiConfig.SystemPromptPath)
	}
	if promptPath != "" {
		fullPromptPath := promptPath
		if !filepath.IsAbs(fullPromptPath) && req.WorkspaceDir != "" {
			fullPromptPath = filepath.Join(req.WorkspaceDir, promptPath)
		}
		if data, readErr := os.ReadFile(fullPromptPath); readErr == nil && len(strings.TrimSpace(string(data))) > 0 {
			sysPrompt = string(data) + "\n\n" + DefaultAIReviewSystemPrompt
		}
	}

	filesToReview := req.TouchedFiles
	if len(filesToReview) == 0 && req.WorkspaceDir != "" {
		_ = filepath.WalkDir(req.WorkspaceDir, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(req.WorkspaceDir, p)
			if relErr == nil && !strings.HasPrefix(rel, ".niebla") && !strings.HasPrefix(rel, ".git") {
				filesToReview = append(filesToReview, filepath.ToSlash(rel))
			}
			return nil
		})
	}

	maxLines := e.aiConfig.MaxContextLines
	if maxLines <= 0 {
		maxLines = 1200
	}
	var userPrompt strings.Builder
	fmt.Fprintf(&userPrompt, "Review the following modified files in Changelist %d (Patchset %d):\n\n", req.CLID, req.Patchset)
	for _, relPath := range filesToReview {
		if len(req.Agent.TriggerPaths) > 0 && !req.Agent.MatchesAnyFile([]string{relPath}) {
			continue
		}
		absPath := filepath.Join(req.WorkspaceDir, filepath.FromSlash(relPath))
		contentBytes, readErr := os.ReadFile(absPath)
		if readErr != nil {
			continue
		}
		lines := strings.Split(string(contentBytes), "\n")
		if len(lines) > maxLines {
			lines = lines[:maxLines]
		}
		fmt.Fprintf(&userPrompt, "=== FILE: %s ===\n", relPath)
		for i, line := range lines {
			fmt.Fprintf(&userPrompt, "%4d | %s\n", i+1, line)
		}
		userPrompt.WriteString("\n")
	}

	respJSON, err := aiClient.CompletePrompt(ctx, sysPrompt, userPrompt.String())
	if err != nil {
		return []byte(err.Error()), err
	}
	return []byte(respJSON), nil
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
