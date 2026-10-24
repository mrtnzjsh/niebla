package cli

import (
	"os"
	"testing"
)

func TestResolveServerAddr(t *testing.T) {
	_ = os.Unsetenv(EnvCitcServer)

	// 1. Fallback to default
	addr := ResolveServerAddr("")
	if addr != DefaultServerAddr {
		t.Errorf("expected %s, got %s", DefaultServerAddr, addr)
	}

	// 2. Env variable override
	_ = os.Setenv(EnvCitcServer, "server.corp.local:50051")
	defer os.Unsetenv(EnvCitcServer)

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
