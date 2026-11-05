package db

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// isLocalDatabase returns true if rawURL targets the local machine (localhost, 127.0.0.1, or unix socket).
func isLocalDatabase(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return strings.Contains(rawURL, "host=127.0.0.1") ||
			strings.Contains(rawURL, "host=localhost") ||
			strings.Contains(rawURL, "host=/") ||
			!strings.Contains(rawURL, "host=")
	}
	host := u.Hostname()
	return host == "" || host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasPrefix(u.Host, "/")
}

// tryNativePostgres attempts to initialize and start a local PostgreSQL cluster via pg_ctl and initdb.
func tryNativePostgres(ctx context.Context, port int) (string, error) {
	initdbPath, err := exec.LookPath("initdb")
	if err != nil {
		return "", fmt.Errorf("initdb not found in PATH: %w", err)
	}
	pgctlPath, err := exec.LookPath("pg_ctl")
	if err != nil {
		return "", fmt.Errorf("pg_ctl not found in PATH: %w", err)
	}

	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	clusterDir := filepath.Join(dataHome, "niebla", "postgres")

	// 1. Initialize cluster if not present
	if _, err := os.Stat(filepath.Join(clusterDir, "PG_VERSION")); os.IsNotExist(err) {
		if err := os.MkdirAll(clusterDir, 0700); err != nil {
			return "", fmt.Errorf("failed to create postgres data dir %s: %w", clusterDir, err)
		}
		log.Printf("[auto-db] Initializing new PostgreSQL cluster in %s...", clusterDir)
		initCmd := exec.CommandContext(ctx, initdbPath, "-D", clusterDir, "--auth=trust", "-U", "niebla", "--no-instructions")
		if out, err := initCmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("initdb failed: %s (%w)", strings.TrimSpace(string(out)), err)
		}
	}

	// 2. Start PostgreSQL via pg_ctl
	log.Printf("[auto-db] Starting PostgreSQL on port %d via pg_ctl...", port)
	startCmd := exec.CommandContext(ctx, pgctlPath, "-D", clusterDir, "-o", fmt.Sprintf("-p %d -k /tmp", port), "start", "-w")
	if out, err := startCmd.CombinedOutput(); err != nil {
		// Verify if it is already running
		statusCmd := exec.CommandContext(ctx, pgctlPath, "-D", clusterDir, "status")
		if statusErr := statusCmd.Run(); statusErr != nil {
			return "", fmt.Errorf("pg_ctl start failed: %s (%w)", strings.TrimSpace(string(out)), err)
		}
	}

	// 3. Ensure database exists
	createdbPath, _ := exec.LookPath("createdb")
	if createdbPath != "" {
		createCmd := exec.CommandContext(ctx, createdbPath, "-h", "127.0.0.1", "-p", fmt.Sprintf("%d", port), "-U", "niebla", "niebla")
		_ = createCmd.Run()
	}

	return fmt.Sprintf("postgres://niebla:niebla@127.0.0.1:%d/niebla?sslmode=disable", port), nil
}

// tryContainerPostgres attempts to run or start a PostgreSQL container using Docker or Podman.
func tryContainerPostgres(ctx context.Context, port int) (string, error) {
	runtime, err := exec.LookPath("docker")
	if err != nil {
		runtime, err = exec.LookPath("podman")
		if err != nil {
			return "", fmt.Errorf("neither docker nor podman found in PATH")
		}
	}

	containerName := "niebla-postgres"
	inspectCmd := exec.CommandContext(ctx, runtime, "container", "inspect", containerName)
	if inspectCmd.Run() == nil {
		log.Printf("[auto-db] Starting existing %s container %q...", filepath.Base(runtime), containerName)
		startCmd := exec.CommandContext(ctx, runtime, "start", containerName)
		if out, err := startCmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("failed to start existing container: %s (%w)", strings.TrimSpace(string(out)), err)
		}
	} else {
		log.Printf("[auto-db] Spawning new %s container %q on port %d...", filepath.Base(runtime), containerName, port)
		runCmd := exec.CommandContext(ctx, runtime, "run", "-d",
			"--name", containerName,
			"-p", fmt.Sprintf("%d:5432", port),
			"-e", "POSTGRES_USER=niebla",
			"-e", "POSTGRES_PASSWORD=niebla",
			"-e", "POSTGRES_DB=niebla",
			"-v", "niebla-postgres-data:/var/lib/postgresql/data",
			"postgres:16-alpine",
		)
		if out, err := runCmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("failed to run postgres container: %s (%w)", strings.TrimSpace(string(out)), err)
		}
	}

	// Wait up to 15 seconds for PostgreSQL port to accept connections
	deadline := time.Now().Add(15 * time.Second)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			time.Sleep(500 * time.Millisecond) // Give postgres a moment to initialize database
			return fmt.Sprintf("postgres://niebla:niebla@127.0.0.1:%d/niebla?sslmode=disable", port), nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	return "", fmt.Errorf("timed out waiting for %s container to listen on %s", containerName, addr)
}

// AutoSpawnPostgres attempts to provision and start a local PostgreSQL instance
// on port 5433 using pg_ctl/initdb or Docker/Podman container.
func AutoSpawnPostgres(ctx context.Context) (string, error) {
	port := 5433

	// 1. Try native postgres binaries (e.g. Nix wrapped or system installed)
	if u, err := tryNativePostgres(ctx, port); err == nil {
		log.Printf("[auto-db] Native PostgreSQL cluster active on port %d", port)
		return u, nil
	}

	// 2. Try container runtime (Docker / Podman)
	if u, err := tryContainerPostgres(ctx, port); err == nil {
		log.Printf("[auto-db] Containerized PostgreSQL instance active on port %d", port)
		return u, nil
	}

	return "", fmt.Errorf("failed to auto-spawn local PostgreSQL: neither native pg_ctl nor docker/podman succeeded")
}

