package auth

import (
	"net/http/httptest"
	"testing"

	"citc-toolchain/internal/config"
)

func TestLocalResolver(t *testing.T) {
	cfg := config.CritiqueAuthConfig{
		Mode:        "local",
		DefaultUser: "matatan",
	}
	resolver := NewUserIdentityResolver(cfg)

	// 1. Default case
	req := httptest.NewRequest("GET", "/cl/1", nil)
	user := resolver.ResolveUser(req)
	if user != "matatan" {
		t.Errorf("expected 'matatan', got %q", user)
	}

	// 2. Query param override
	reqWithQuery := httptest.NewRequest("GET", "/cl/1?reviewer=alice", nil)
	overrideUser := resolver.ResolveUser(reqWithQuery)
	if overrideUser != "alice" {
		t.Errorf("expected 'alice' from query param, got %q", overrideUser)
	}
}

func TestHeaderResolver(t *testing.T) {
	cfg := config.CritiqueAuthConfig{
		Mode:        "headers",
		HeaderName:  "X-Forwarded-User",
		DefaultUser: "fallback_user",
	}
	resolver := NewUserIdentityResolver(cfg)

	// 1. Header present
	reqWithHeader := httptest.NewRequest("GET", "/cl/1", nil)
	reqWithHeader.Header.Set("X-Forwarded-User", "bob@corp.internal")
	user := resolver.ResolveUser(reqWithHeader)
	if user != "bob@corp.internal" {
		t.Errorf("expected 'bob@corp.internal' from header, got %q", user)
	}

	// 2. Header absent (fallback to default)
	reqNoHeader := httptest.NewRequest("GET", "/cl/1", nil)
	fallback := resolver.ResolveUser(reqNoHeader)
	if fallback != "fallback_user" {
		t.Errorf("expected 'fallback_user' when header is absent, got %q", fallback)
	}

	// 3. Query param override
	reqWithQuery := httptest.NewRequest("GET", "/cl/1?reviewer=override_bob", nil)
	queryUser := resolver.ResolveUser(reqWithQuery)
	if queryUser != "override_bob" {
		t.Errorf("expected 'override_bob' from query param, got %q", queryUser)
	}
}

func TestTailscaleResolver(t *testing.T) {
	cfg := config.CritiqueAuthConfig{
		Mode:        "tailscale",
		DefaultUser: "matatan",
	}
	resolver := NewUserIdentityResolver(cfg)

	// 1. Tailscale header injected
	req := httptest.NewRequest("GET", "/cl/1", nil)
	req.Header.Set("Tailscale-User-Login", "charlie@tailnet.ts.net")
	user := resolver.ResolveUser(req)
	if user != "charlie@tailnet.ts.net" {
		t.Errorf("expected 'charlie@tailnet.ts.net', got %q", user)
	}

	// 2. Fallback
	reqEmpty := httptest.NewRequest("GET", "/cl/1", nil)
	fallback := resolver.ResolveUser(reqEmpty)
	if fallback != "matatan" {
		t.Errorf("expected 'matatan', got %q", fallback)
	}
}
