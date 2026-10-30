package grpc

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestAuthenticator_PermissiveWhenDisabled(t *testing.T) {
	// When tokens is empty, authentication is disabled (permissive local development)
	auth := NewAuthenticator(nil)
	if auth.IsEnabled() {
		t.Errorf("expected authenticator to be disabled")
	}

	ctx := context.Background()
	if err := auth.ValidateContext(ctx); err != nil {
		t.Errorf("expected nil error in permissive mode, got: %v", err)
	}

	interceptor := auth.UnaryServerInterceptor()
	called := false
	handler := func(ctx context.Context, req any) (any, error) {
		called = true
		return "ok", nil
	}
	resp, err := interceptor(ctx, "req", &grpc.UnaryServerInfo{}, handler)
	if err != nil || resp != "ok" || !called {
		t.Errorf("unary interceptor failed in permissive mode: err=%v, called=%v", err, called)
	}
}

func TestAuthenticator_BearerTokenValidation(t *testing.T) {
	auth := NewAuthenticator([]string{"secret-token-123", "ci-runner-token"})
	if !auth.IsEnabled() {
		t.Fatalf("expected authenticator to be enabled")
	}

	tests := []struct {
		name          string
		md            metadata.MD
		expectedCode  codes.Code
		expectSuccess bool
	}{
		{
			name:          "missing metadata",
			md:            nil,
			expectedCode:  codes.Unauthenticated,
			expectSuccess: false,
		},
		{
			name:          "empty metadata",
			md:            metadata.Pairs(),
			expectedCode:  codes.Unauthenticated,
			expectSuccess: false,
		},
		{
			name:          "invalid token in authorization header",
			md:            metadata.Pairs("authorization", "Bearer invalid-token"),
			expectedCode:  codes.Unauthenticated,
			expectSuccess: false,
		},
		{
			name:          "malformed authorization header",
			md:            metadata.Pairs("authorization", "Basic secret-token-123"),
			expectedCode:  codes.Unauthenticated,
			expectSuccess: false,
		},
		{
			name:          "valid token in authorization header",
			md:            metadata.Pairs("authorization", "Bearer secret-token-123"),
			expectSuccess: true,
		},
		{
			name:          "valid token in authorization header case-insensitive",
			md:            metadata.Pairs("authorization", "bearer ci-runner-token"),
			expectSuccess: true,
		},
		{
			name:          "valid token in x-niebla-token header",
			md:            metadata.Pairs("x-niebla-token", "secret-token-123"),
			expectSuccess: true,
		},
		{
			name:          "invalid token in x-niebla-token header",
			md:            metadata.Pairs("x-niebla-token", "wrong-token"),
			expectedCode:  codes.Unauthenticated,
			expectSuccess: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.md != nil {
				ctx = metadata.NewIncomingContext(ctx, tc.md)
			}

			err := auth.ValidateContext(ctx)
			if tc.expectSuccess {
				if err != nil {
					t.Errorf("expected success, got error: %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				s, ok := status.FromError(err)
				if !ok || s.Code() != tc.expectedCode {
					t.Errorf("expected status code %v, got %v", tc.expectedCode, s.Code())
				}
			}
		})
	}
}

type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func TestAuthenticator_StreamServerInterceptor(t *testing.T) {
	auth := NewAuthenticator([]string{"stream-token"})

	// 1. Rejected stream
	streamReject := &mockServerStream{ctx: context.Background()}
	streamInterceptor := auth.StreamServerInterceptor()
	err := streamInterceptor(nil, streamReject, &grpc.StreamServerInfo{}, func(srv any, stream grpc.ServerStream) error {
		return nil
	})
	if err == nil || status.Code(err) != codes.Unauthenticated {
		t.Errorf("expected Unauthenticated on unauthenticated stream, got: %v", err)
	}

	// 2. Accepted stream
	ctxValid := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer stream-token"))
	streamAccept := &mockServerStream{ctx: ctxValid}
	streamCalled := false
	err = streamInterceptor(nil, streamAccept, &grpc.StreamServerInfo{}, func(srv any, stream grpc.ServerStream) error {
		streamCalled = true
		return nil
	})
	if err != nil || !streamCalled {
		t.Errorf("expected stream handler to succeed: err=%v, called=%v", err, streamCalled)
	}
}
