package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"niebla/internal/config"
	"niebla/pkg/httpclient"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

const (
	// DefaultServerAddr is the default gRPC endpoint for niebla-server.
	DefaultServerAddr = "127.0.0.1:50051"

	// EnvNieblaServer is the environment variable name to override server endpoint.
	EnvNieblaServer = "NIEBLA_SERVER"

	// EnvNieblaToken is the environment variable name for bearer token authentication.
	EnvNieblaToken = "NIEBLA_TOKEN"
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

// ResolveToken determines the authentication token with the following precedence:
// 1. Explicit token (if non-empty)
// 2. NIEBLA_TOKEN environment variable (if set)
// 3. Config core.auth_token (if set)
func ResolveToken(explicitToken string, cfg *config.Config) string {
	if explicitToken != "" {
		return explicitToken
	}
	if env := os.Getenv(EnvNieblaToken); env != "" {
		return env
	}
	if cfg != nil && cfg.Core.AuthToken != "" {
		return cfg.Core.AuthToken
	}
	return ""
}

// TokenAuth implements credentials.PerRPCCredentials to attach a bearer token to outgoing RPCs.
type TokenAuth struct {
	Token      string
	RequireTLS bool
}

// GetRequestMetadata attaches the Authorization Bearer header.
func (t TokenAuth) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + t.Token,
	}, nil
}

// RequireTransportSecurity returns whether transport encryption is required for sending credentials.
func (t TokenAuth) RequireTransportSecurity() bool {
	return t.RequireTLS
}

// DialOption configures DialGRPC.
type DialOption func(*dialOptions)

type dialOptions struct {
	tlsConfig *config.ClientTLSConfig
	token     string
}

// WithTLSConfig overrides or provides client TLS configuration.
func WithTLSConfig(tlsCfg *config.ClientTLSConfig) DialOption {
	return func(o *dialOptions) {
		o.tlsConfig = tlsCfg
	}
}

// WithToken specifies the bearer token to use for gRPC authentication.
func WithToken(token string) DialOption {
	return func(o *dialOptions) {
		o.token = token
	}
}

// BuildClientCredentials constructs gRPC transport credentials based on ClientTLSConfig.
// If tlsCfg is nil or not enabled, insecure plaintext credentials are returned.
func BuildClientCredentials(tlsCfg *config.ClientTLSConfig) (credentials.TransportCredentials, error) {
	if tlsCfg == nil || !tlsCfg.Enabled {
		return insecure.NewCredentials(), nil
	}

	tlsConfig, err := httpclient.BuildTLSConfig(httpclient.TLSOptions{
		Enabled:    tlsCfg.Enabled,
		CAFile:     tlsCfg.CAFile,
		ServerName: tlsCfg.ServerName,
	})
	if err != nil {
		return nil, err
	}

	return credentials.NewTLS(tlsConfig), nil
}

// DialGRPC dials the Niebla server using gRPC with keepalive settings, optional TLS, and optional Bearer auth.
func DialGRPC(ctx context.Context, serverAddr string, opts ...DialOption) (*grpc.ClientConn, nieblav1.NieblaServiceClient, error) {
	var dOpts dialOptions
	for _, opt := range opts {
		opt(&dOpts)
	}

	var cfg *config.Config
	if dOpts.tlsConfig == nil || dOpts.token == "" {
		cfg, _ = config.Load()
	}

	tlsCfg := dOpts.tlsConfig
	if tlsCfg == nil && cfg != nil && cfg.Core.TLS.Enabled {
		tlsCfg = &cfg.Core.TLS
	}

	token := ResolveToken(dOpts.token, cfg)

	transportCreds, err := BuildClientCredentials(tlsCfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build transport credentials: %w", err)
	}

	kacp := keepalive.ClientParameters{
		Time:                10 * time.Second,
		Timeout:             3 * time.Second,
		PermitWithoutStream: true,
	}

	grpcOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(transportCreds),
		grpc.WithKeepaliveParams(kacp),
		grpc.WithBlock(),
	}

	if token != "" {
		requireTLS := tlsCfg != nil && tlsCfg.Enabled
		grpcOpts = append(grpcOpts, grpc.WithPerRPCCredentials(TokenAuth{
			Token:      token,
			RequireTLS: requireTLS,
		}))
	}

	conn, err := grpc.DialContext(ctx, serverAddr, grpcOpts...)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to Niebla server at %s: %w", serverAddr, err)
	}

	client := nieblav1.NewNieblaServiceClient(conn)
	return conn, client, nil
}
