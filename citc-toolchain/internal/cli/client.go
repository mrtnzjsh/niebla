package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	citcv1 "citc-toolchain/proto/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

const (
	// DefaultServerAddr is the default gRPC endpoint for citc-server.
	DefaultServerAddr = "127.0.0.1:50051"

	// EnvCitcServer is the environment variable name to override server endpoint.
	EnvCitcServer = "CITC_SERVER"
)

// ResolveServerAddr determines the target server address with the following precedence:
// 1. Explicit flag value (if non-empty)
// 2. CITC_SERVER environment variable (if set)
// 3. Default "127.0.0.1:50051"
func ResolveServerAddr(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv(EnvCitcServer); env != "" {
		return env
	}
	return DefaultServerAddr
}

// DialGRPC dials the CitC server using gRPC with keepalive settings.
func DialGRPC(ctx context.Context, serverAddr string) (*grpc.ClientConn, citcv1.RepositoryServiceClient, error) {
	kacp := keepalive.ClientParameters{
		Time:                10 * time.Second,
		Timeout:             3 * time.Second,
		PermitWithoutStream: true,
	}

	conn, err := grpc.DialContext(
		ctx,
		serverAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(kacp),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to CitC server at %s: %w", serverAddr, err)
	}

	client := citcv1.NewRepositoryServiceClient(conn)
	return conn, client, nil
}
