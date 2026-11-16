//go:build rc

package rc_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"niebla/assets"
	"niebla/internal/config"
	"niebla/internal/db"
	"niebla/internal/groups"
	internalgrpc "niebla/internal/grpc"
	"niebla/internal/web/auth"
	"niebla/internal/web/handlers"
	"niebla/pkg/cas"
	pkgevents "niebla/pkg/events"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// RCTestCluster encapsulates an end-to-end containerized environment
// including PostgreSQL, gRPC server, CAS storage, and the Cotejo web server.
type RCTestCluster struct {
	DB             *db.DB
	CASStorage     *cas.Storage
	CASDir         string
	GRPCServer     *internalgrpc.Server
	GRPCServerAddr string
	GRPCConn       *grpc.ClientConn
	GRPCClient     nieblav1.NieblaServiceClient
	CotejoServer   *httptest.Server
	CotejoMux      *http.ServeMux
	Config         *config.Config
	cleanupFunc    func()
}

// SetupRCCluster spins up a complete, isolated release candidate cluster
// with PostgreSQL running in a Docker container via testcontainers-go.
func SetupRCCluster(t *testing.T) *RCTestCluster {
	t.Helper()
	ctx := context.Background()

	// 1. PostgreSQL 16 Testcontainer
	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("niebla_rc_test"),
		postgres.WithUsername("niebla_rc_user"),
		postgres.WithPassword("niebla_rc_password"),
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

	// 2. Connect DB & execute all migrations
	cfg := db.DefaultConfig(connStr)
	database, err := db.Connect(ctx, cfg)
	if err != nil {
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to connect to test db and run migrations: %v", err)
	}

	// 3. Central CAS storage
	casDir := filepath.Join(t.TempDir(), "niebla-rc-cas")
	casStorage, err := cas.NewStorage(casDir)
	if err != nil {
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to initialize CAS storage: %v", err)
	}

	// 4. Start gRPC server on a free TCP port
	grpcListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to bind gRPC listener: %v", err)
	}

	grpcServerInstance, err := internalgrpc.NewServer(database, casStorage, 1000)
	if err != nil {
		_ = grpcListener.Close()
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to create internal gRPC server: %v", err)
	}

	rawGRPCServer := grpc.NewServer()
	nieblav1.RegisterNieblaServiceServer(rawGRPCServer, grpcServerInstance)
	go func() {
		_ = rawGRPCServer.Serve(grpcListener)
	}()

	grpcAddr := grpcListener.Addr().String()

	// 5. Connect gRPC client
	grpcConn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		rawGRPCServer.Stop()
		_ = grpcListener.Close()
		database.Close()
		_ = pgContainer.Terminate(ctx)
		t.Fatalf("failed to dial gRPC server at %s: %v", grpcAddr, err)
	}
	grpcClient := nieblav1.NewNieblaServiceClient(grpcConn)

	// 6. Cotejo Web HTTP Server
	appConfig := config.DefaultConfig()
	appConfig.Critique.Groups = config.CritiqueGroupConfig{
		Mode: "static",
		Static: map[string][]string{
			"backend-core": {"alice", "bob"},
			"ai-team":      {"agent/review-agent", "agent/ai-hint-agent"},
		},
	}
	groupResolver := groups.NewResolver(appConfig.Critique.Groups)
	authResolver := &auth.HeaderResolver{
		HeaderName:     "X-Remote-User",
		TrustedProxies: []string{"*"},
	}

	eventEmitter := pkgevents.NewNoopEmitter()

	clHandler := handlers.NewCLHandler(database, casStorage, authResolver)
	clHandler.SetEmitter(eventEmitter)
	clHandler.SetDependencies(appConfig, groupResolver)

	commentsHandler := handlers.NewCommentsHandler(database, authResolver)
	commentsHandler.SetDependencies(casStorage, appConfig)

	rawHandler := handlers.NewRawHandler(database, casStorage)
	ciHandler := handlers.NewCIHandler(database)

	mux := http.NewServeMux()
	mux.Handle("/static/", assets.Handler(""))
	mux.HandleFunc("GET /{$}", clHandler.HandleIndex)
	mux.HandleFunc("GET /cls", clHandler.HandleIndex)
	mux.HandleFunc("GET /cl/{id}", clHandler.HandleCLView)
	mux.HandleFunc("GET /cl/{id}/diff-content", clHandler.HandleCLDiffContent)
	mux.HandleFunc("GET /cl/{id}/file/{path...}", clHandler.HandleCLFileDiff)
	mux.HandleFunc("POST /cl/{id}/approval", clHandler.HandleApproval)
	mux.HandleFunc("POST /cl/{id}/file-review", clHandler.HandleFileReview)
	mux.HandleFunc("POST /cl/{id}/reviewers", clHandler.HandleAddReviewers)
	mux.HandleFunc("DELETE /cl/{id}/reviewers/{user}", clHandler.HandleRemoveReviewer)
	mux.HandleFunc("POST /cl/{id}/submit", clHandler.HandleSubmitCL)
	mux.HandleFunc("POST /cl/{id}/reply", clHandler.HandlePublishReview)
	mux.HandleFunc("POST /cl/{id}/publish", clHandler.HandlePublishReview)

	mux.HandleFunc("GET /cl/{id}/comment-box", commentsHandler.HandleGetCommentBox)
	mux.HandleFunc("POST /cl/{id}/comments", commentsHandler.HandleCreateComment)
	mux.HandleFunc("POST /cl/{id}/ai-hint/generate", commentsHandler.HandleGenerateAIHint)
	mux.HandleFunc("POST /cl/{id}/comments/{comment_id}/resolve", commentsHandler.HandleResolveComment)
	mux.HandleFunc("POST /cl/{id}/comments/{comment_id}/discard", commentsHandler.HandleDiscardComment)
	mux.HandleFunc("POST /cl/{id}/comments/{comment_id}/apply-suggestion", commentsHandler.HandleApplySuggestion)
	mux.HandleFunc("POST /cl/{id}/comments/{comment_id}/reject-suggestion", commentsHandler.HandleRejectSuggestion)

	mux.HandleFunc("POST /api/v1/ci/status", ciHandler.HandlePostCheckRunStatus)
	mux.HandleFunc("GET /api/v1/ci/status", ciHandler.HandleGetCheckRuns)
	mux.HandleFunc("GET /cl/{id}/checks", ciHandler.HandleGetCheckRuns)
	mux.HandleFunc("GET /cl/{id}/raw", rawHandler.ServeRawBlob)

	cotejoServer := httptest.NewServer(mux)

	cleanup := func() {
		cotejoServer.Close()
		_ = grpcConn.Close()
		rawGRPCServer.Stop()
		_ = grpcListener.Close()
		database.Close()
		_ = pgContainer.Terminate(context.Background())
		_ = os.RemoveAll(casDir)
	}

	return &RCTestCluster{
		DB:             database,
		CASStorage:     casStorage,
		CASDir:         casDir,
		GRPCServer:     grpcServerInstance,
		GRPCServerAddr: grpcAddr,
		GRPCConn:       grpcConn,
		GRPCClient:     grpcClient,
		CotejoServer:   cotejoServer,
		CotejoMux:      mux,
		Config:         appConfig,
		cleanupFunc:    cleanup,
	}
}

// Cleanup tears down all running servers, clients, and testcontainers.
func (c *RCTestCluster) Cleanup() {
	if c.cleanupFunc != nil {
		c.cleanupFunc()
	}
}
