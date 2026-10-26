package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	nieblav1 "niebla/pkg/proto/nieblav1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

const (
	// DefaultServerAddr is the default gRPC endpoint for niebla-server.
	DefaultServerAddr = "127.0.0.1:50051"

	// EnvNieblaServer is the environment variable name to override server endpoint.
	EnvNieblaServer = "NIEBLA_SERVER"
)

// ResolveServerAddr determines the target server address with the following precedence:
// 1. Explicit flag value (if non-empty)
// 2. NIEBLA_SERVER environment variable (if set)
// 3. Default "127.0.0.1:50051"
func ResolveServerAddr(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if env := os.Getenv(EnvNieblaServer); env != "" {
		return env
	}
	return DefaultServerAddr
}

// DialGRPC dials the Niebla server using gRPC with keepalive settings.
func DialGRPC(ctx context.Context, serverAddr string) (*grpc.ClientConn, nieblav1.NieblaServiceClient, error) {
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
		return nil, nil, fmt.Errorf("failed to connect to Niebla server at %s: %w", serverAddr, err)
	}

	client := nieblav1.NewNieblaServiceClient(conn)
	return conn, client, nil
}
