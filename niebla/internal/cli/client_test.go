package cli

import (
	"context"
	"os"
	"testing"

	"niebla/internal/config"
)

func TestResolveServerAddr(t *testing.T) {
	_ = os.Unsetenv(EnvNieblaServer)

	// 1. Fallback to default
	addr := ResolveServerAddr("")
	if addr != DefaultServerAddr {
		t.Errorf("expected %s, got %s", DefaultServerAddr, addr)
	}

	// 2. Env variable override
	_ = os.Setenv(EnvNieblaServer, "server.corp.local:50051")
	defer os.Unsetenv(EnvNieblaServer)

	addr = ResolveServerAddr("")
	if addr != "server.corp.local:50051" {
		t.Errorf("expected server.corp.local:50051 from env, got %s", addr)
	}

	// 3. Flag takes highest precedence
	addr = ResolveServerAddr("custom-flag-host:9999")
	if addr != "custom-flag-host:9999" {
		t.Errorf("expected custom-flag-host:9999 from flag, got %s", addr)
	}
}

func TestResolveToken(t *testing.T) {
	_ = os.Unsetenv(EnvNieblaToken)

	// 1. Empty fallback
	if tok := ResolveToken("", nil); tok != "" {
		t.Errorf("expected empty token, got %q", tok)
	}

	// 2. Config fallback
	cfg := &config.Config{
		Core: config.CoreConfig{AuthToken: "config-secret"},
	}
	if tok := ResolveToken("", cfg); tok != "config-secret" {
		t.Errorf("expected config-secret from config, got %q", tok)
	}

	// 3. Env variable takes precedence over config
	_ = os.Setenv(EnvNieblaToken, "env-secret")
	defer os.Unsetenv(EnvNieblaToken)
	if tok := ResolveToken("", cfg); tok != "env-secret" {
		t.Errorf("expected env-secret from env, got %q", tok)
	}

	// 4. Explicit token takes highest precedence
	if tok := ResolveToken("flag-secret", cfg); tok != "flag-secret" {
		t.Errorf("expected flag-secret from explicit parameter, got %q", tok)
	}
}

func TestTokenAuth(t *testing.T) {
	ta := TokenAuth{
		Token:      "my-test-token",
		RequireTLS: true,
	}

	if !ta.RequireTransportSecurity() {
		t.Errorf("expected RequireTransportSecurity = true")
	}

	md, err := ta.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md["authorization"] != "Bearer my-test-token" {
		t.Errorf("expected Bearer my-test-token, got %q", md["authorization"])
	}
}

func TestBuildClientCredentials(t *testing.T) {
	// 1. Nil or disabled returns insecure credentials
	creds, err := BuildClientCredentials(nil)
	if err != nil || creds == nil {
		t.Fatalf("expected valid insecure creds for nil config, err=%v", err)
	}
	if creds.Info().SecurityProtocol != "insecure" {
		t.Errorf("expected insecure protocol, got %s", creds.Info().SecurityProtocol)
	}

	// 2. Enabled without CAFile uses system roots
	tlsCfg := &config.ClientTLSConfig{
		Enabled:    true,
		ServerName: "niebla.internal",
	}
	creds, err = BuildClientCredentials(tlsCfg)
	if err != nil || creds == nil {
		t.Fatalf("expected valid TLS creds, err=%v", err)
	}
	if creds.Info().SecurityProtocol != "tls" {
		t.Errorf("expected tls protocol, got %s", creds.Info().SecurityProtocol)
	}

	// 3. Enabled with nonexistent CA file returns error
	tlsCfgBad := &config.ClientTLSConfig{
		Enabled: true,
		CAFile:  "/nonexistent/ca.crt",
	}
	_, err = BuildClientCredentials(tlsCfgBad)
	if err == nil {
		t.Errorf("expected error for nonexistent CA file")
	}
}
