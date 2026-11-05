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

	// 3. Query param override in local mode
	reqWithQuery := httptest.NewRequest("GET", "/cl/1?reviewer=alice", nil)
	overrideUser := resolverExplicit.ResolveUser(reqWithQuery)
	if overrideUser != "alice" {
		t.Errorf("expected 'alice' from query param, got %q", overrideUser)
	}
}

func TestHeaderResolver(t *testing.T) {
	cfg := config.CritiqueAuthConfig{
		Mode:               "headers",
		HeaderName:         "X-Forwarded-User",
		DefaultUser:        "fallback_user",
		TrustedProxies:     []string{"127.0.0.1", "10.0.0.0/8"},
		AllowQueryOverride: false,
	}
	resolver := NewUserIdentityResolver(cfg)

	// 1. Header present from trusted loopback proxy
	reqTrusted := httptest.NewRequest("GET", "/cl/1", nil)
	reqTrusted.RemoteAddr = "127.0.0.1:45678"
	reqTrusted.Header.Set("X-Forwarded-User", "bob@corp.internal")
	user := resolver.ResolveUser(reqTrusted)
	if user != "bob@corp.internal" {
		t.Errorf("expected 'bob@corp.internal' from header via trusted proxy, got %q", user)
	}

	// 2. Header present from trusted CIDR proxy
	reqCIDR := httptest.NewRequest("GET", "/cl/1", nil)
	reqCIDR.RemoteAddr = "10.1.2.3:45678"
	reqCIDR.Header.Set("X-Forwarded-User", "bob@corp.internal")
	userCIDR := resolver.ResolveUser(reqCIDR)
	if userCIDR != "bob@corp.internal" {
		t.Errorf("expected 'bob@corp.internal' from CIDR proxy, got %q", userCIDR)
	}

	// 3. Header spoofing from untrusted remote address must be rejected
	reqUntrusted := httptest.NewRequest("GET", "/cl/1", nil)
	reqUntrusted.RemoteAddr = "198.51.100.25:12345"
	reqUntrusted.Header.Set("X-Forwarded-User", "attacker@evil.com")
	spoofed := resolver.ResolveUser(reqUntrusted)
	if spoofed != "fallback_user" {
		t.Errorf("expected fallback_user for spoofed untrusted header, got %q", spoofed)
	}

	// 4. Query param override ignored when AllowQueryOverride is false
	reqQueryIgnored := httptest.NewRequest("GET", "/cl/1?reviewer=override_bob", nil)
	reqQueryIgnored.RemoteAddr = "127.0.0.1:45678"
	queryUser := resolver.ResolveUser(reqQueryIgnored)
	if queryUser != "fallback_user" {
		t.Errorf("expected 'fallback_user' when query override is disabled, got %q", queryUser)
	}

	// 5. Query param override honored when AllowQueryOverride is true
	cfgAllowed := cfg
	cfgAllowed.AllowQueryOverride = true
	resolverAllowed := NewUserIdentityResolver(cfgAllowed)
	reqQueryHonored := httptest.NewRequest("GET", "/cl/1?reviewer=override_bob", nil)
	reqQueryHonored.RemoteAddr = "127.0.0.1:45678"
	queryUserAllowed := resolverAllowed.ResolveUser(reqQueryHonored)
	if queryUserAllowed != "override_bob" {
		t.Errorf("expected 'override_bob' when query override is enabled, got %q", queryUserAllowed)
	}
}

func TestTailscaleResolver(t *testing.T) {
	cfg := config.CritiqueAuthConfig{
		Mode:               "tailscale",
		DefaultUser:        "fallback_user",
		AllowQueryOverride: false,
	}
	resolver := NewUserIdentityResolver(cfg)

	// 1. Authenticated header from loopback (tailscale serve)
	reqLoopback := httptest.NewRequest("GET", "/cl/1", nil)
	reqLoopback.RemoteAddr = "127.0.0.1:54321"
	reqLoopback.Header.Set("Tailscale-User-Login", "charlie@tailnet.ts.net")
	user := resolver.ResolveUser(reqLoopback)
	if user != "charlie@tailnet.ts.net" {
		t.Errorf("expected 'charlie@tailnet.ts.net', got %q", user)
	}

	// 2. Spoofed Tailscale header from untrusted external IP rejected
	reqUntrusted := httptest.NewRequest("GET", "/cl/1", nil)
	reqUntrusted.RemoteAddr = "203.0.113.50:54321"
	reqUntrusted.Header.Set("Tailscale-User-Login", "attacker@tailnet.ts.net")
	spoofed := resolver.ResolveUser(reqUntrusted)
	if spoofed != "fallback_user" {
		t.Errorf("expected 'fallback_user' for spoofed header, got %q", spoofed)
	}

	// 3. Fallback when no header and no WhoIs
	reqEmpty := httptest.NewRequest("GET", "/cl/1", nil)
	reqEmpty.RemoteAddr = "127.0.0.1:54321"
	fallback := resolver.ResolveUser(reqEmpty)
	if fallback != "fallback_user" {
		t.Errorf("expected 'fallback_user', got %q", fallback)
	}
}
