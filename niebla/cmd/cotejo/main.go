package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"niebla/internal/config"
	"niebla/internal/db"
	internalevents "niebla/internal/events"
	"niebla/internal/groups"
	"niebla/internal/web/auth"
	"niebla/internal/web/diff"
	"niebla/internal/web/handlers"
	"niebla/internal/web/views"
	"niebla/pkg/cas"
)

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			return parsed
		}
	}
	return defaultVal
}

func main() {
	configFlag := flag.String("config", "", "Path to configuration file (defaults to ~/.config/niebla/config.toml)")
	hostFlag := flag.String("host", "", "Host address for Cotejo web service (overrides config)")
	portFlag := flag.Int("port", 0, "Port number for Cotejo web service (overrides config)")
	dbURLFlag := flag.String("db-url", "", "PostgreSQL connection string (overrides config)")
	blobPathFlag := flag.String("blob-path", "", "Path to CAS blob storage (overrides config)")
	assetsDirFlag := flag.String("assets-dir", getEnv("COTEJO_ASSETS_DIR", "./assets"), "Path to assets directory")
	flag.Parse()

	// 0. Load and validate unified configuration
	var cfg *config.Config
	var err error
	if *configFlag != "" {
		cfg, err = config.LoadFromPath(*configFlag)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		log.Fatalf("❌ Configuration error:\n%v", err)
	}

	// Apply explicit command line flag overrides if provided
	if *hostFlag != "" {
		cfg.Critique.Host = *hostFlag
	}
	if *portFlag != 0 {
		cfg.Critique.Port = *portFlag
	}
	if *dbURLFlag != "" {
		cfg.Server.DBURL = *dbURLFlag
	}
	if *blobPathFlag != "" {
		cfg.Server.BlobPath = *blobPathFlag
	}

	// Fast-fail re-validation in case CLI flags introduced invalid parameters
	if err := config.Validate(cfg); err != nil {
		log.Fatalf("❌ Configuration validation error:\n%v", err)
	}

	// Resolve active theme and wire into Critique views & Chroma syntax highlighting
	activeTheme := config.ResolveTheme(cfg.Critique.Theme)
	views.SetActiveThemeCSS(activeTheme.ToCSSVariables())
	diff.SetSyntaxStyle(activeTheme.SyntaxStyle)

	log.Printf("Starting Cotejo Code Review Web Service...")
	log.Printf("Target bind address : %s:%d", cfg.Critique.Host, cfg.Critique.Port)
	log.Printf("Live reload proxy   : port %d", cfg.Critique.ProxyPort)
	log.Printf("Theme Preset        : %s (syntax: %s)", activeTheme.Name, activeTheme.SyntaxStyle)
	log.Printf("Default Reviewer    : %s", cfg.Critique.DefaultReviewer)
	log.Printf("CAS blob path       : %s", cfg.Server.BlobPath)
	log.Printf("PostgreSQL URL      : %s", cfg.Server.DBURL)
	log.Printf("Assets directory    : %s", *assetsDirFlag)

	// 1. Initialize CAS local blob storage engine
	casStorage, err := cas.NewStorage(cfg.Server.BlobPath)
	if err != nil {
		log.Printf("Warning: CAS storage initialization at %s returned: %v (will retry or use fallback)", cfg.Server.BlobPath, err)
		fallbackDir := filepath.Join(os.TempDir(), "niebla_blobs")
		casStorage, err = cas.NewStorage(fallbackDir)
		if err != nil {
			log.Fatalf("Failed to initialize CAS storage engine: %v", err)
		}
		log.Printf("Using fallback CAS directory: %s", fallbackDir)
	}

	// 2. Connect to database & apply migrations (or fallback to UI Dev Mode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbConfig := db.DefaultConfig(cfg.Server.DBURL)
	database, err := db.Connect(ctx, dbConfig)
	if err != nil {
		log.Printf("⚠️  PostgreSQL connection unavailable (%v)", err)
		log.Printf("🚀 Starting Cotejo in standalone UI Dev Mode with seeded CLs.")
		log.Printf("   Interactive review, Myers diffs, and HTMX comments are active.")
		database = nil
	} else {
		defer database.Close()
		log.Printf("Connected to PostgreSQL and verified Cotejo schema migrations.")
	}

	// 3. Initialize HTTP handlers and event publisher
	eventEmitter, err := internalevents.NewEmitterFromConfig(&cfg.Events)
	if err != nil {
		log.Fatalf("Failed to initialize event publisher: %v", err)
	}
	defer eventEmitter.Close()

	if cfg.Events.Enabled {
		log.Printf("Event Publishing    : enabled (sink: %s)", cfg.Events.Sink)
	} else {
		log.Printf("Event Publishing    : disabled (local use)")
	}

	authResolver := auth.NewUserIdentityResolver(cfg.Critique.Auth)
	clHandler := handlers.NewCLHandler(database, casStorage, authResolver)
	clHandler.SetEmitter(eventEmitter)
	groupResolver := groups.NewResolver(cfg.Critique.Groups)
	clHandler.SetDependencies(cfg, groupResolver)
	commentsHandler := handlers.NewCommentsHandler(database, authResolver)
	commentsHandler.SetDependencies(casStorage, cfg)
	rawHandler := handlers.NewRawHandler(database, casStorage)
	ciHandler := handlers.NewCIHandler(database)

	// 4. Set up HTTP routing (Go 1.22+ ServeMux with method and pattern matching)
	mux := http.NewServeMux()

	// Static assets
	staticDir := filepath.Join(*assetsDirFlag, "static")
	if _, err := os.Stat(staticDir); os.IsNotExist(err) {
		// Try relative to current working directory
		staticDir = "./assets/static"
	}
	fs := http.FileServer(http.Dir(staticDir))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Review routes
	mux.HandleFunc("GET /{$}", clHandler.HandleIndex)
	mux.HandleFunc("GET /cls", clHandler.HandleIndex)

	// CL detail & diff views
	mux.HandleFunc("GET /cl/{id}", clHandler.HandleCLView)
	mux.HandleFunc("GET /cl/{id}/diff-content", clHandler.HandleCLDiffContent)
	mux.HandleFunc("GET /cl/{id}/file/{path...}", clHandler.HandleCLFileDiff)
	mux.HandleFunc("POST /cl/{id}/approval", clHandler.HandleApproval)
	mux.HandleFunc("POST /cl/{id}/file-review", clHandler.HandleFileReview)
	mux.HandleFunc("POST /cl/{id}/reviewers", clHandler.HandleAddReviewers)
	mux.HandleFunc("POST /cl/{id}/reviewers/{user}/remove", clHandler.HandleRemoveReviewer)
	mux.HandleFunc("DELETE /cl/{id}/reviewers/{user}", clHandler.HandleRemoveReviewer)
	mux.HandleFunc("POST /cl/{id}/submit", clHandler.HandleSubmitCL)
	mux.HandleFunc("POST /cl/{id}/reply", clHandler.HandlePublishReview)
	mux.HandleFunc("POST /cl/{id}/publish", clHandler.HandlePublishReview)

	// Inline comments & AI hints HTMX endpoints
	mux.HandleFunc("GET /cl/{id}/comment-box", commentsHandler.HandleGetCommentBox)
	mux.HandleFunc("POST /cl/{id}/comments", commentsHandler.HandleCreateComment)
	mux.HandleFunc("POST /cl/{id}/ai-hint/generate", commentsHandler.HandleGenerateAIHint)
	mux.HandleFunc("POST /cl/{id}/comments/{comment_id}/resolve", commentsHandler.HandleResolveComment)
	mux.HandleFunc("POST /cl/{id}/comments/{comment_id}/discard", commentsHandler.HandleDiscardComment)
	mux.HandleFunc("POST /cl/{id}/comments/{comment_id}/apply-suggestion", commentsHandler.HandleApplySuggestion)
	mux.HandleFunc("POST /cl/{id}/comments/{comment_id}/reject-suggestion", commentsHandler.HandleRejectSuggestion)

	// CI Check Runs API
	mux.HandleFunc("POST /api/v1/ci/status", ciHandler.HandlePostCheckRunStatus)
	mux.HandleFunc("GET /api/v1/ci/status", ciHandler.HandleGetCheckRuns)
	mux.HandleFunc("GET /cl/{id}/checks", ciHandler.HandleGetCheckRuns)

	// Raw blobs
	mux.HandleFunc("GET /cl/{id}/raw", rawHandler.ServeRawBlob)
	mux.HandleFunc("GET /raw", rawHandler.ServeRawBlob)

	// Health check
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	// 5. Start HTTP server
	addr := fmt.Sprintf("%s:%d", cfg.Critique.Host, cfg.Critique.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", addr, err)
	}
	log.Printf("Cotejo web review service listening at http://%s", addr)

	serverErrCh := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrCh <- err
		}
		close(serverErrCh)
	}()

	// 6. Graceful shutdown on SIGINT / SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("Received signal %s, initiating graceful shutdown...", sig)
	case err := <-serverErrCh:
		log.Fatalf("Cotejo HTTP server error: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	if database != nil {
		log.Println("Closing database connections...")
		database.Close()
	}
	_ = eventEmitter.Close()
	log.Println("Cotejo web server stopped gracefully.")
}
