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

	"citc-toolchain/internal/db"
	"citc-toolchain/internal/web/handlers"
	"citc-toolchain/pkg/cas"
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
	hostFlag := flag.String("host", getEnv("CITC_HOST", "0.0.0.0"), "Host address for Critique web service")
	portFlag := flag.Int("port", getEnvInt("CRITIQUE_PORT", 8080), "Port number for Critique web service")
	dbURLFlag := flag.String("db-url", getEnv("DATABASE_URL", "postgres:///citc?host=/run/postgresql"), "PostgreSQL connection string")
	blobPathFlag := flag.String("blob-path", getEnv("CITC_BLOB_PATH", "/devpool/citc_blobs"), "Path to CAS blob storage")
	assetsDirFlag := flag.String("assets-dir", getEnv("CITC_ASSETS_DIR", "./assets"), "Path to assets directory")
	flag.Parse()

	log.Printf("Starting Critique Code Review Web Service...")
	log.Printf("Target bind address : %s:%d", *hostFlag, *portFlag)
	log.Printf("CAS blob path       : %s", *blobPathFlag)
	log.Printf("PostgreSQL URL      : %s", *dbURLFlag)
	log.Printf("Assets directory    : %s", *assetsDirFlag)

	// 1. Initialize CAS local blob storage engine
	casStorage, err := cas.NewStorage(*blobPathFlag)
	if err != nil {
		log.Printf("Warning: CAS storage initialization at %s returned: %v (will retry or use fallback)", *blobPathFlag, err)
		// Fallback to local temporary directory if default not accessible
		fallbackDir := filepath.Join(os.TempDir(), "citc_blobs")
		casStorage, err = cas.NewStorage(fallbackDir)
		if err != nil {
			log.Fatalf("Failed to initialize CAS storage engine: %v", err)
		}
		log.Printf("Using fallback CAS directory: %s", fallbackDir)
	}

	// 2. Connect to database & apply migrations (or fallback to UI Dev Mode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbConfig := db.DefaultConfig(*dbURLFlag)
	database, err := db.Connect(ctx, dbConfig)
	if err != nil {
		log.Printf("⚠️  PostgreSQL connection unavailable (%v)", err)
		log.Printf("🚀 Starting Critique in standalone UI Dev Mode with seeded CLs.")
		log.Printf("   Interactive review, Myers diffs, and HTMX comments are active.")
		database = nil
	} else {
		defer database.Close()
		log.Printf("Connected to PostgreSQL and verified Critique schema migrations.")
	}

	// 3. Initialize HTTP handlers
	clHandler := handlers.NewCLHandler(database, casStorage)
	commentsHandler := handlers.NewCommentsHandler(database)
	rawHandler := handlers.NewRawHandler(database, casStorage)

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
	mux.HandleFunc("GET /cl/{id}/file/{path...}", clHandler.HandleCLFileDiff)
	mux.HandleFunc("POST /cl/{id}/approval", clHandler.HandleApproval)

	// Inline comments HTMX endpoints
	mux.HandleFunc("GET /cl/{id}/comment-box", commentsHandler.HandleGetCommentBox)
	mux.HandleFunc("POST /cl/{id}/comments", commentsHandler.HandleCreateComment)
	mux.HandleFunc("POST /cl/{id}/comments/{comment_id}/resolve", commentsHandler.HandleResolveComment)

	// Raw blobs
	mux.HandleFunc("GET /cl/{id}/raw", rawHandler.ServeRawBlob)
	mux.HandleFunc("GET /raw", rawHandler.ServeRawBlob)

	// Health check
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	// 5. Start HTTP server
	addr := fmt.Sprintf("%s:%d", *hostFlag, *portFlag)
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
	log.Printf("Critique web review service listening at http://%s", addr)

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
		log.Fatalf("Critique HTTP server error: %v", err)
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
	log.Println("Critique web server stopped gracefully.")
}
