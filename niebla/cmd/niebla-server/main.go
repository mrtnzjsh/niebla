package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"niebla/internal/config"
	"niebla/internal/db"
	internalevents "niebla/internal/events"
	internalgrpc "niebla/internal/grpc"
	"niebla/pkg/cas"
	pkgevents "niebla/pkg/events"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
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
	hostFlag := flag.String("host", getEnv("NIEBLA_HOST", "0.0.0.0"), "Host address for gRPC service")
	portFlag := flag.Int("port", getEnvInt("NIEBLA_PORT", 50051), "Port number for gRPC service")
	dbURLFlag := flag.String("db-url", getEnv("DATABASE_URL", "postgres:///niebla?host=/run/postgresql"), "PostgreSQL connection string")
	blobPathFlag := flag.String("blob-path", getEnv("NIEBLA_BLOB_PATH", "/devpool/niebla_blobs"), "Path to CAS blob storage")
	cacheSizeFlag := flag.Int("cache-size", 10000, "Maximum number of tree nodes in LRU cache")
	tlsFlag := flag.Bool("tls", false, "Enable TLS transport security")
	tlsCertFlag := flag.String("tls-cert", getEnv("NIEBLA_TLS_CERT", ""), "Path to TLS server certificate file")
	tlsKeyFlag := flag.String("tls-key", getEnv("NIEBLA_TLS_KEY", ""), "Path to TLS server private key file")
	tokenFlag := flag.String("token", getEnv("NIEBLA_TOKEN", ""), "Optional authorization token (enables bearer auth)")
	flag.Parse()

	log.Printf("Initializing niebla-server...")
	log.Printf("Target bind address : %s:%d", *hostFlag, *portFlag)
	log.Printf("CAS blob path       : %s", *blobPathFlag)
	log.Printf("PostgreSQL URL      : %s", *dbURLFlag)

	// 1. Initialize CAS local blob storage engine
	casStorage, err := cas.NewStorage(*blobPathFlag)
	if err != nil {
		log.Fatalf("Failed to initialize CAS storage engine at %s: %v", *blobPathFlag, err)
	}

	// 2. Connect to database & run embedded schema migrations
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbConfig := db.DefaultConfig(*dbURLFlag)
	database, err := db.Connect(ctx, dbConfig)
	if err != nil {
		log.Fatalf("Failed to connect to database and apply migrations: %v", err)
	}
	defer database.Close()
	log.Printf("Connected to PostgreSQL and verified schema migrations.")

	// 3. Load toolchain configuration
	cfg, _ := config.Load()

	// 4. Build gRPC server options (TLS & Bearer Token Authentication)
	var grpcOpts []grpc.ServerOption

	// Determine TLS configuration (CLI flags take precedence over config.toml)
	tlsEnabled := *tlsFlag
	certFile := *tlsCertFlag
	keyFile := *tlsKeyFlag
	if !tlsEnabled && cfg != nil && cfg.Server.TLS.Enabled {
		tlsEnabled = true
		certFile = cfg.Server.TLS.CertFile
		keyFile = cfg.Server.TLS.KeyFile
	}

	if tlsEnabled {
		if certFile == "" || keyFile == "" {
			log.Fatalf("TLS enabled but certificate or key file not specified (use --tls-cert and --tls-key or server.tls in config)")
		}
		creds, err := credentials.NewServerTLSFromFile(certFile, keyFile)
		if err != nil {
			log.Fatalf("Failed to load TLS credentials: %v", err)
		}
		grpcOpts = append(grpcOpts, grpc.Creds(creds))
		log.Printf("TLS 1.3 transport security enabled (cert: %s)", certFile)
	} else {
		log.Printf("Running gRPC server with insecure transport (plaintext loopback)")
	}

	// Determine Auth Tokens
	var authTokens []string
	if *tokenFlag != "" {
		authTokens = append(authTokens, *tokenFlag)
	}
	if cfg != nil && len(cfg.Server.AuthTokens) > 0 {
		authTokens = append(authTokens, cfg.Server.AuthTokens...)
	}

	auth := internalgrpc.NewAuthenticator(authTokens)
	if auth.IsEnabled() {
		grpcOpts = append(grpcOpts,
			grpc.UnaryInterceptor(auth.UnaryServerInterceptor()),
			grpc.StreamInterceptor(auth.StreamServerInterceptor()),
		)
		log.Printf("Bearer token authentication enforced (%d valid token(s) configured)", len(authTokens))
	}

	grpcServer := grpc.NewServer(grpcOpts...)
	repoServer, err := internalgrpc.NewServer(database, casStorage, *cacheSizeFlag)
	if err != nil {
		log.Fatalf("Failed to create NieblaService server: %v", err)
	}

	var emitter pkgevents.Emitter
	if cfg != nil {
		repoServer.SetToolchainConfig(cfg)
		if em, err := internalevents.NewEmitterFromConfig(&cfg.Events); err == nil {
			emitter = em
			repoServer.SetEmitter(em)
		}
	}
	if emitter != nil {
		defer emitter.Close()
	}

	repoServer.Register(grpcServer)

	// 4. Start network listener
	addr := fmt.Sprintf("%s:%d", *hostFlag, *portFlag)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to listen on %s: %v", addr, err)
	}
	log.Printf("niebla-server gRPC service listening on %s", addr)

	serverErrCh := make(chan error, 1)
	go func() {
		if err := grpcServer.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			serverErrCh <- err
		}
		close(serverErrCh)
	}()

	// 5. Handle graceful shutdown on SIGINT / SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Printf("Received signal %s, initiating graceful shutdown...", sig)
	case err := <-serverErrCh:
		log.Fatalf("gRPC server error: %v", err)
	}

	// Graceful stop with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		log.Println("gRPC server gracefully stopped.")
	case <-shutdownCtx.Done():
		log.Println("Graceful shutdown timeout exceeded, forcing stop.")
		grpcServer.Stop()
	}

	log.Println("Draining database connection pool...")
	database.Close()
	log.Println("niebla-server shutdown complete.")
}
