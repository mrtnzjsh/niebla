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
	"path/filepath"
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

func buildGRPCServerOptions(cfg *config.Config, tlsFlag bool, tlsCertFlag, tlsKeyFlag, tokenFlag string) []grpc.ServerOption {
	var grpcOpts []grpc.ServerOption

	tlsEnabled := tlsFlag
	certFile := tlsCertFlag
	keyFile := tlsKeyFlag
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

	var authTokens []string
	if tokenFlag != "" {
		authTokens = append(authTokens, tokenFlag)
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

	return grpcOpts
}

func loadServerConfig(configFlag, hostFlag, dbURLFlag, blobPathFlag string, portFlag, cacheSizeFlag int) *config.Config {
	var cfg *config.Config
	var err error
	if configFlag != "" {
		cfg, err = config.LoadFromPath(configFlag)
	} else {
		cfg, err = config.Load()
	}
	if err != nil {
		log.Printf("Warning: failed to load configuration from %s (%v); using defaults", configFlag, err)
		cfg = config.DefaultConfig()
	}

	if hostFlag != "" {
		cfg.Server.Host = hostFlag
	}
	if portFlag != 0 {
		cfg.Server.Port = portFlag
	}
	if dbURLFlag != "" {
		cfg.Server.DBURL = dbURLFlag
	}
	if blobPathFlag != "" {
		cfg.Server.BlobPath = blobPathFlag
	}
	if cacheSizeFlag != 0 {
		cfg.Server.CacheSize = cacheSizeFlag
	}

	cfg.Server.BlobPath = os.ExpandEnv(cfg.Server.BlobPath)
	cfg.FS.MountBase = os.ExpandEnv(cfg.FS.MountBase)
	cfg.FS.CacheDir = os.ExpandEnv(cfg.FS.CacheDir)

	return cfg
}

func main() {
	configFlag := flag.String("config", "", "Path to configuration file (defaults to ~/.config/niebla/config.toml)")
	hostFlag := flag.String("host", "", "Host address for gRPC service (overrides config)")
	portFlag := flag.Int("port", 0, "Port number for gRPC service (overrides config)")
	dbURLFlag := flag.String("db-url", "", "PostgreSQL connection string (overrides config)")
	blobPathFlag := flag.String("blob-path", "", "Path to CAS blob storage (overrides config)")
	cacheSizeFlag := flag.Int("cache-size", 0, "Maximum number of tree nodes in LRU cache (overrides config)")
	autoDBFlag := flag.Bool("auto-db", true, "Automatically spawn local PostgreSQL if not running on loopback")
	tlsFlag := flag.Bool("tls", false, "Enable TLS transport security")
	tlsCertFlag := flag.String("tls-cert", getEnv("NIEBLA_TLS_CERT", ""), "Path to TLS server certificate file")
	tlsKeyFlag := flag.String("tls-key", getEnv("NIEBLA_TLS_KEY", ""), "Path to TLS server private key file")
	tokenFlag := flag.String("token", getEnv("NIEBLA_TOKEN", ""), "Optional authorization token (enables bearer auth)")
	flag.Parse()

	cfg := loadServerConfig(*configFlag, *hostFlag, *dbURLFlag, *blobPathFlag, *portFlag, *cacheSizeFlag)

	log.Printf("Initializing niebla-server...")
	log.Printf("Target bind address : %s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("CAS blob path       : %s", cfg.Server.BlobPath)
	log.Printf("PostgreSQL URL      : %s", cfg.Server.DBURL)
	log.Printf("Auto-DB provision   : %v", *autoDBFlag && cfg.Server.ShouldAutoDB())

	casStorage, err := cas.NewStorage(cfg.Server.BlobPath)
	if err != nil {
		log.Printf("Warning: CAS storage initialization at %s returned: %v (falling back to temporary dir)", cfg.Server.BlobPath, err)
		fallbackDir := filepath.Join(os.TempDir(), "niebla_blobs")
		casStorage, err = cas.NewStorage(fallbackDir)
		if err != nil {
			log.Fatalf("Failed to initialize CAS storage engine: %v", err)
		}
		log.Printf("Using fallback CAS directory: %s", fallbackDir)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbConfig := db.DefaultConfig(cfg.Server.DBURL)
	dbConfig.AutoSpawn = *autoDBFlag && cfg.Server.ShouldAutoDB()
	database, err := db.Connect(ctx, dbConfig)
	if err != nil {
		log.Fatalf("Failed to connect to database and apply migrations: %v", err)
	}
	defer database.Close()
	log.Printf("Connected to PostgreSQL and verified schema migrations.")

	grpcOpts := buildGRPCServerOptions(cfg, *tlsFlag, *tlsCertFlag, *tlsKeyFlag, *tokenFlag)

	grpcServer := grpc.NewServer(grpcOpts...)
	repoServer, err := internalgrpc.NewServer(database, casStorage, cfg.Server.CacheSize)
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
