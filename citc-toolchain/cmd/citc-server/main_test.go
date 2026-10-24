package main

import (
	"os"
	"testing"
)

func TestGetEnv(t *testing.T) {
	const key = "TEST_CITC_ENV_VAR"
	_ = os.Unsetenv(key)

	// Test default fallback
	val := getEnv(key, "default_val")
	if val != "default_val" {
		t.Errorf("expected default_val, got %s", val)
	}

	// Test environment override
	_ = os.Setenv(key, "custom_val")
	defer os.Unsetenv(key)

	val = getEnv(key, "default_val")
	if val != "custom_val" {
		t.Errorf("expected custom_val, got %s", val)
	}
}

func TestGetEnvInt(t *testing.T) {
	const key = "TEST_CITC_PORT_VAR"
	_ = os.Unsetenv(key)

	// Test default fallback
	port := getEnvInt(key, 50051)
	if port != 50051 {
		t.Errorf("expected 50051, got %d", port)
	}

	// Test valid integer in env
	_ = os.Setenv(key, "8080")
	defer os.Unsetenv(key)

	port = getEnvInt(key, 50051)
	if port != 8080 {
		t.Errorf("expected 8080, got %d", port)
	}

	// Test invalid integer in env falls back to default
	_ = os.Setenv(key, "not-a-number")
	port = getEnvInt(key, 50051)
	if port != 50051 {
		t.Errorf("expected fallback 50051 for invalid int, got %d", port)
	}
}
