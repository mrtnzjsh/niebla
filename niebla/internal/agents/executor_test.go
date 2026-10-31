package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"niebla/internal/config"
	"niebla/internal/db"
	pkgagents "niebla/pkg/agents"
	"niebla/pkg/cas"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestEnvironment(t *testing.T) (*db.DB, *cas.Storage, string, func()) {
	t.Helper()
	ctx := context.Background()

	// 1. PostgreSQL Testcontainer
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("niebla_agent_test"),
		postgres.WithUsername("niebla_agent_user"),
		postgres.WithPassword("niebla_agent_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to get connection string: %v", err)
	}

	database, err := db.Connect(ctx, db.DefaultConfig(connStr))
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to connect to db: %v", err)
	}

	// 2. CAS Storage in temp dir
	tempDir := t.TempDir()
	casDir := filepath.Join(tempDir, "cas")
	casStorage, err := cas.NewStorage(casDir)
	if err != nil {
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to initialize CAS storage: %v", err)
	}

	toolsDir := filepath.Join(tempDir, "tools")

	cleanup := func() {
		database.Close()
		_ = pgContainer.Terminate(ctx)
	}

	return database, casStorage, toolsDir, cleanup
}

func TestExecutor_PassingInTreeScript(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	database, casStorage, toolsDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// Create test CL
	clID, err := database.CreateShelvedCL(ctx, "dev", "test changelist", []byte{0x01}, 0)
	if err != nil {
		t.Fatalf("failed to create CL: %v", err)
	}

	wsDir := t.TempDir()
	scriptDir := filepath.Join(wsDir, "tools", "agents")
	_ = os.MkdirAll(scriptDir, 0755)

	scriptPath := filepath.Join(scriptDir, "builder.sh")
	scriptContent := "#!/bin/sh\necho 'build completed successfully'\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	executor := NewExecutor(database, casStorage, toolsDir)

	agentDef := pkgagents.AgentDef{
		Name:        "package-builder",
		DisplayName: "Package Build Runner",
		Type:        "in_tree",
		Entrypoint:  "tools/agents/builder.sh",
		AutoApprove: true,
		CaptureLogs: true,
	}

	res, err := executor.Execute(ctx, ExecutionRequest{
		CLID:         clID,
		Patchset:     1,
		Agent:        agentDef,
		WorkspaceDir: wsDir,
	})
	if err != nil {
		t.Fatalf("executor.Execute failed: %v", err)
	}

	if !res.Passed {
		t.Fatalf("expected execution to pass, got error: %v", res.Error)
	}
	if len(res.LogBlobSHA) == 0 {
		t.Fatalf("expected LogBlobSHA to be populated")
	}

	// 1. Verify check run status in database
	runs, err := database.ListCheckRunsForPatchset(ctx, clID, 1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("expected 1 check run, got %d (err: %v)", len(runs), err)
	}
	if runs[0].Status != "passed" || runs[0].RunnerName != "package-builder" {
		t.Errorf("unexpected check run: %+v", runs[0])
	}

	// 2. Verify auto-approval was cast (verdict = 'lgtm')
	approvals, err := database.ListApprovals(ctx, clID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("expected 1 approval, got %d (err: %v)", len(approvals), err)
	}
	if approvals[0].Reviewer != "package-builder" || approvals[0].Verdict != "lgtm" {
		t.Errorf("expected lgtm approval from package-builder, got: %+v", approvals[0])
	}
}

func TestExecutor_FailingScriptWithDiagnostics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	database, casStorage, toolsDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	clID, err := database.CreateShelvedCL(ctx, "dev", "test changelist", []byte{0x02}, 0)
	if err != nil {
		t.Fatalf("failed to create CL: %v", err)
	}

	wsDir := t.TempDir()
	scriptDir := filepath.Join(wsDir, "tools", "agents")
	_ = os.MkdirAll(scriptDir, 0755)

	scriptPath := filepath.Join(scriptDir, "linter.sh")
	scriptContent := `#!/bin/sh
echo "pkg/auth/token.go:42:15: undefined: ValidateExpiration"
echo "src/main.c:10: error: missing semicolon"
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	executor := NewExecutor(database, casStorage, toolsDir)

	agentDef := pkgagents.AgentDef{
		Name:                  "sec-linter",
		DisplayName:           "Security Linter",
		Type:                  "in_tree",
		Entrypoint:            "tools/agents/linter.sh",
		AutoApprove:           true,
		CaptureLogs:           true,
		PostInlineDiagnostics: true,
	}

	res, err := executor.Execute(ctx, ExecutionRequest{
		CLID:         clID,
		Patchset:     1,
		Agent:        agentDef,
		WorkspaceDir: wsDir,
	})
	if err != nil {
		t.Fatalf("executor.Execute failed: %v", err)
	}

	if res.Passed {
		t.Fatalf("expected execution to fail")
	}
	if len(res.Diagnostics) != 2 {
		t.Fatalf("expected 2 extracted diagnostics, got %d", len(res.Diagnostics))
	}

	// 1. Verify check run status = 'failed'
	runs, err := database.ListCheckRunsForPatchset(ctx, clID, 1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("expected 1 check run, got %d (err: %v)", len(runs), err)
	}
	if runs[0].Status != "failed" || runs[0].RunnerName != "sec-linter" {
		t.Errorf("unexpected check run: %+v", runs[0])
	}

	// 2. Verify approval verdict = 'changes_requested'
	approvals, err := database.ListApprovals(ctx, clID)
	if err != nil || len(approvals) != 1 {
		t.Fatalf("expected 1 approval, got %d (err: %v)", len(approvals), err)
	}
	if approvals[0].Reviewer != "sec-linter" || approvals[0].Verdict != "changes_requested" {
		t.Errorf("expected changes_requested approval from sec-linter, got: %+v", approvals[0])
	}

	// 3. Verify inline comments were posted to Cotejo database
	comments, err := database.ListAllComments(ctx, clID)
	if err != nil || len(comments) != 2 {
		t.Fatalf("expected 2 inline comments in database, got %d (err: %v)", len(comments), err)
	}

	c1 := comments[0]
	if c1.FilePath != "pkg/auth/token.go" || c1.LineNumber != 42 || c1.Author != "sec-linter" {
		t.Errorf("unexpected comment 1: %+v", c1)
	}
	if !strings.Contains(c1.Body, "ValidateExpiration") {
		t.Errorf("comment body missing diagnostic message: %s", c1.Body)
	}
}

func TestExecutor_AIPersonaWithStructuredSuggestion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	database, casStorage, toolsDir, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	clID, err := database.CreateShelvedCL(ctx, "dev", "add form input", []byte{0x03}, 0)
	if err != nil {
		t.Fatalf("failed to create CL: %v", err)
	}

	mockOpenAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "{\"verdict\":\"changes_requested\",\"summary\":\"Missing aria-label on input\",\"comments\":[{\"path\":\"internal/web/views/form.templ\",\"line_start\":3,\"line_end\":3,\"severity\":\"warning\",\"message\":\"Add aria-label for screen readers.\",\"suggestion_code\":\"<input type=\\\"text\\\" name=\\\"q\\\" aria-label=\\\"Search\\\" />\"}]}"
				}
			}]
		}`))
	}))
	defer mockOpenAI.Close()

	wsDir := t.TempDir()
	viewDir := filepath.Join(wsDir, "internal", "web", "views")
	_ = os.MkdirAll(viewDir, 0755)
	_ = os.WriteFile(filepath.Join(viewDir, "form.templ"), []byte("package views\ntempl Form() {\n\t<input type=\"text\" name=\"q\" />\n}\n"), 0644)

	aiCfg := &config.CritiqueAIConfig{
		Enabled:  true,
		Endpoint: mockOpenAI.URL,
		Model:    "gpt-4o-mini",
	}

	executor := NewExecutor(database, casStorage, toolsDir).WithAIConfig(aiCfg)
	res, err := executor.Execute(ctx, ExecutionRequest{
		CLID:         clID,
		Patchset:     1,
		WorkspaceDir: wsDir,
		TouchedFiles: []string{"internal/web/views/form.templ"},
		Agent: pkgagents.AgentDef{
			Name:                  "a11y-bot",
			DisplayName:           "Accessibility Review Bot",
			Type:                  "ai",
			PostInlineDiagnostics: true,
			CaptureLogs:           true,
		},
	})
	if err != nil {
		t.Fatalf("executor.Execute failed: %v", err)
	}
	if res.Passed {
		t.Fatalf("expected changes_requested verdict to mark execution as failed")
	}

	comments, err := database.ListAllComments(ctx, clID)
	if err != nil || len(comments) != 1 {
		t.Fatalf("expected 1 inline comment with suggestion, got %d (err: %v)", len(comments), err)
	}
	if !comments[0].HasSuggestion || !strings.Contains(comments[0].SuggestionCode, "aria-label=\"Search\"") {
		t.Errorf("expected comment to have suggestion_code populated, got: %+v", comments[0])
	}
}
