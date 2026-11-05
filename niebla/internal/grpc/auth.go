package grpc

import (
	"context"
	"crypto/subtle"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// HeaderAuthorization is the standard gRPC / HTTP authorization header name.
const HeaderAuthorization = "authorization"

// HeaderNieblaToken is an alternative custom header name for bearer tokens.
const HeaderNieblaToken = "x-niebla-token"

// Authenticator validates incoming RPC credentials against a configured set of authorized bearer tokens.
type Authenticator struct {
	tokens []string
}

// NewAuthenticator initializes an Authenticator with a slice of authorized bearer tokens.
// If tokens is empty, authentication is disabled (permissive local development mode).
func NewAuthenticator(tokens []string) *Authenticator {
	var clean []string
	for _, token := range tokens {
		trimmed := strings.TrimSpace(token)
		if trimmed != "" {
			clean = append(clean, trimmed)
		}
	}
	return &Authenticator{tokens: clean}
}

// IsEnabled returns true if token authentication is enabled (i.e. at least one valid token is configured).
func (a *Authenticator) IsEnabled() bool {
	return a != nil && len(a.tokens) > 0
}

// matchToken checks if providedToken matches any valid token using subtle.ConstantTimeCompare
// to prevent timing attacks.
func (a *Authenticator) matchToken(provided string) bool {
	providedBytes := []byte(strings.TrimSpace(provided))
	matched := false
	for _, valid := range a.tokens {
		validBytes := []byte(valid)
		if subtle.ConstantTimeCompare(providedBytes, validBytes) == 1 {
			matched = true
		}
	}
	return matched
}

// ValidateContext extracts credentials from gRPC incoming metadata and asserts validity against configured tokens.
func (a *Authenticator) ValidateContext(ctx context.Context) error {
	if !a.IsEnabled() {
		return nil
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "authentication required: missing request metadata")
	}

	if authHeaders := md.Get(HeaderAuthorization); len(authHeaders) > 0 {
		for _, header := range authHeaders {
			parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
			if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
				if a.matchToken(parts[1]) {
					return nil
				}
			}
		}
	}

	if tokenHeaders := md.Get(HeaderNieblaToken); len(tokenHeaders) > 0 {
		for _, token := range tokenHeaders {
			if a.matchToken(token) {
				return nil
			}
		}
	}

	return status.Error(codes.Unauthenticated, "authentication required: invalid or missing bearer token")
}

// UnaryServerInterceptor returns a grpc.UnaryServerInterceptor that enforces token authentication.
func (a *Authenticator) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		if err := a.ValidateContext(ctx); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamServerInterceptor returns a grpc.StreamServerInterceptor that enforces token authentication.
func (a *Authenticator) StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if err := a.ValidateContext(ss.Context()); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}
