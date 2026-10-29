package handlers

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
	"niebla/internal/groups"
	"niebla/internal/web/auth"
	"niebla/pkg/cas"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestDBForInbox(t *testing.T) (*db.DB, func()) {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("niebla_inbox_test"),
		postgres.WithUsername("niebla_user"),
		postgres.WithPassword("niebla_password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres test container: %v", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to get container connection string: %v", err)
	}

	cfg := db.DefaultConfig(connStr)
	database, err := db.Connect(ctx, cfg)
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to connect to test database and run migrations: %v", err)
	}

	cleanup := func() {
		database.Close()
		_ = pgContainer.Terminate(context.Background())
	}

	return database, cleanup
}

func TestCotejoInboxSegregation_IntegrationWithPostgres(t *testing.T) {
	if testing.Short() || os.Getenv("CI") != "" {
		t.Skip("skipping testcontainer integration test in short/CI mode")
	}

	database, cleanupDB := setupTestDBForInbox(t)
	defer cleanupDB()

	ctx := context.Background()
	casDir := filepath.Join(t.TempDir(), "cas")
	casStorage, err := cas.NewStorage(casDir)
	if err != nil {
		t.Fatalf("failed to create CAS storage: %v", err)
	}

	// 1. Configure Critique with group resolution
	cfg := config.DefaultConfig()
	cfg.Critique.Groups = config.CritiqueGroupConfig{
		Mode: "static",
		Static: map[string][]string{
			"backend": {"alice", "bob"},
		},
	}
	groupResolver := groups.NewResolver(cfg.Critique.Groups)

	emptyTreeHash := []byte{0x01, 0x02, 0x03, 0x04}

	// 2. Seed changelists
	// CL 10: Authored by alice (shelved)
	_, err = database.CreateShelvedCL(ctx, "alice", "feat: alice feature", emptyTreeHash, 1)
	if err != nil {
		t.Fatalf("failed to create CL 10: %v", err)
	}

	// CL 11: Authored by charlie, reviewer is @backend (alice & bob)
	cl11ID, err := database.CreateShelvedCL(ctx, "charlie", "feat: core api", emptyTreeHash, 1)
	if err != nil {
		t.Fatalf("failed to create CL 11: %v", err)
	}
	if err := database.AddReviewers(ctx, cl11ID, []string{"@backend"}); err != nil {
		t.Fatalf("failed to add reviewer @backend to CL 11: %v", err)
	}

	// CL 12: Authored by charlie, reviewer is bob only
	cl12ID, err := database.CreateShelvedCL(ctx, "charlie", "fix: database index", emptyTreeHash, 1)
	if err != nil {
		t.Fatalf("failed to create CL 12: %v", err)
	}
	if err := database.AddReviewers(ctx, cl12ID, []string{"bob"}); err != nil {
		t.Fatalf("failed to add reviewer bob to CL 12: %v", err)
	}

	// CL 13: Authored by agent/refactor-bot (agent CL)
	_, err = database.CreateShelvedCL(ctx, "agent/refactor-bot", "refactor: rename internal symbols", emptyTreeHash, 1)
	if err != nil {
		t.Fatalf("failed to create CL 13: %v", err)
	}

	// CL 14: Authored by bot/ci (agent CL)
	_, err = database.CreateShelvedCL(ctx, "bot/ci", "ci: update flake inputs", emptyTreeHash, 1)
	if err != nil {
		t.Fatalf("failed to create CL 14: %v", err)
	}

	// CL 15: Authored by dave, submitted to trunk
	cl15ID, err := database.CreateShelvedCL(ctx, "dave", "feat: trunk completed work", emptyTreeHash, 1)
	if err != nil {
		t.Fatalf("failed to create CL 15: %v", err)
	}
	_ = database.UpdateCLStatus(ctx, cl15ID, "submitted")

	// 3. Test Alice's Views
	aliceHandler := NewCLHandler(database, casStorage, &auth.LocalResolver{DefaultUser: "alice"})
	aliceHandler.SetDependencies(cfg, groupResolver)

	// A. Alice's "my-reviews" tab
	reqAliceReviews := httptest.NewRequest("GET", "/?tab=my-reviews", nil)
	rrAliceReviews := httptest.NewRecorder()
	aliceHandler.HandleIndex(rrAliceReviews, reqAliceReviews)

	if rrAliceReviews.Code != http.StatusOK {
		t.Fatalf("alice my-reviews failed: %d", rrAliceReviews.Code)
	}
	aliceReviewsBody := rrAliceReviews.Body.String()

	// Should contain CL 11 (assigned via @backend)
	if !strings.Contains(aliceReviewsBody, "feat: core api") {
		t.Errorf("expected CL 11 (feat: core api) in Alice's my-reviews tab")
	}
	// Should NOT contain CL 10 (she is author), CL 12 (assigned only to bob), or CL 13/14 (agent CLs)
	if strings.Contains(aliceReviewsBody, "feat: alice feature") {
		t.Errorf("Alice's own CL 10 should not be in my-reviews queue")
	}
	if strings.Contains(aliceReviewsBody, "fix: database index") {
		t.Errorf("CL 12 (bob only) should not be in Alice's my-reviews queue")
	}
	if strings.Contains(aliceReviewsBody, "refactor: rename internal symbols") {
		t.Errorf("agent CL 13 should not be in human review queue")
	}

	// B. Alice's "outgoing" tab
	reqAliceOut := httptest.NewRequest("GET", "/?tab=outgoing", nil)
	rrAliceOut := httptest.NewRecorder()
	aliceHandler.HandleIndex(rrAliceOut, reqAliceOut)

	aliceOutBody := rrAliceOut.Body.String()
	if !strings.Contains(aliceOutBody, "feat: alice feature") {
		t.Errorf("expected Alice's CL 10 in outgoing tab")
	}
	if strings.Contains(aliceOutBody, "feat: core api") {
		t.Errorf("charlie's CL 11 should not be in Alice's outgoing tab")
	}

	// C. Agent / Automated tab
	reqAgents := httptest.NewRequest("GET", "/?tab=agents", nil)
	rrAgents := httptest.NewRecorder()
	aliceHandler.HandleIndex(rrAgents, reqAgents)

	agentsBody := rrAgents.Body.String()
	if !strings.Contains(agentsBody, "refactor: rename internal symbols") || !strings.Contains(agentsBody, "ci: update flake inputs") {
		t.Errorf("expected CL 13 and CL 14 in agents tab")
	}
	if !strings.Contains(agentsBody, "🤖 Agent") {
		t.Errorf("expected 🤖 Agent indicator in agents tab")
	}
	if strings.Contains(agentsBody, "feat: alice feature") || strings.Contains(agentsBody, "feat: core api") {
		t.Errorf("human CLs should not be in agents tab")
	}

	// D. Submitted / Trunk tab
	reqSubmitted := httptest.NewRequest("GET", "/?tab=submitted", nil)
	rrSubmitted := httptest.NewRecorder()
	aliceHandler.HandleIndex(rrSubmitted, reqSubmitted)

	subBody := rrSubmitted.Body.String()
	if !strings.Contains(subBody, "feat: trunk completed work") {
		t.Errorf("expected submitted CL 15 in submitted tab")
	}

	// 4. Test Bob's Views (Bob should see both CL 11 via @backend and CL 12 directly)
	bobHandler := NewCLHandler(database, casStorage, &auth.LocalResolver{DefaultUser: "bob"})
	bobHandler.SetDependencies(cfg, groupResolver)

	reqBobReviews := httptest.NewRequest("GET", "/?tab=my-reviews", nil)
	rrBobReviews := httptest.NewRecorder()
	bobHandler.HandleIndex(rrBobReviews, reqBobReviews)

	bobReviewsBody := rrBobReviews.Body.String()
	if !strings.Contains(bobReviewsBody, "feat: core api") {
		t.Errorf("expected CL 11 (via @backend) in Bob's my-reviews tab")
	}
	if !strings.Contains(bobReviewsBody, "fix: database index") {
		t.Errorf("expected CL 12 (direct) in Bob's my-reviews tab")
	}
}
