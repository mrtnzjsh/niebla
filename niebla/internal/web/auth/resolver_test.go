package auth

import (
	"net/http/httptest"
	"testing"

	"niebla/internal/config"
)

func TestLocalResolver(t *testing.T) {
	// 1. Dynamic $USER resolution
	t.Setenv("USER", "test_user")
	cfgEnv := config.CritiqueAuthConfig{
		Mode:        "local",
		DefaultUser: "$USER",
	}
	resolverEnv := NewUserIdentityResolver(cfgEnv)
	req := httptest.NewRequest("GET", "/cl/1", nil)
	user := resolverEnv.ResolveUser(req)
	if user != "test_user" {
		t.Errorf("expected 'test_user' from $USER, got %q", user)
	}

	// 2. Custom configured user
	cfgExplicit := config.CritiqueAuthConfig{
		Mode:        "local",
		DefaultUser: "reviewer_charlie",
	}
	resolverExplicit := NewUserIdentityResolver(cfgExplicit)
	userExplicit := resolverExplicit.ResolveUser(req)
	if userExplicit != "reviewer_charlie" {
		t.Errorf("expected 'reviewer_charlie', got %q", userExplicit)
	}

	// 3. Query param override
	reqWithQuery := httptest.NewRequest("GET", "/cl/1?reviewer=alice", nil)
	overrideUser := resolverExplicit.ResolveUser(reqWithQuery)
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
		DefaultUser: "fallback_user",
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
	if fallback != "fallback_user" {
		t.Errorf("expected 'fallback_user', got %q", fallback)
	}
}
