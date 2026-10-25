package events_test

import (
	"os"
	"testing"

	"citc-toolchain/internal/config"
	internalevents "citc-toolchain/internal/events"
	pkgevents "citc-toolchain/pkg/events"
)

func TestNewEmitterFromConfigDisabled(t *testing.T) {
	cfg := &config.EventsConfig{
		Enabled: false,
	}
	emitter, err := internalevents.NewEmitterFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := emitter.(*pkgevents.NoopEmitter); !ok {
		t.Fatalf("expected *pkgevents.NoopEmitter when events are disabled, got %T", emitter)
	}
	_ = emitter.Close()
}

func TestNewEmitterFromConfigWebhook(t *testing.T) {
	_ = os.Setenv("TEST_WEBHOOK_SECRET", "supersecret")
	defer os.Unsetenv("TEST_WEBHOOK_SECRET")

	cfg := &config.EventsConfig{
		Enabled: true,
		Sink:    "webhook",
		Webhook: config.WebhookSinkConfig{
			Endpoint:       "https://hooks.example.com/citc",
			SecretTokenEnv: "TEST_WEBHOOK_SECRET",
			TimeoutMS:      2000,
			MaxRetries:     2,
		},
	}
	emitter, err := internalevents.NewEmitterFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := emitter.(*pkgevents.AsyncEmitter); !ok {
		t.Fatalf("expected *pkgevents.AsyncEmitter for webhook sink, got %T", emitter)
	}
	_ = emitter.Close()
}

func TestNewEmitterFromConfigExec(t *testing.T) {
	cfg := &config.EventsConfig{
		Enabled: true,
		Sink:    "exec",
		Exec: config.ExecSinkConfig{
			Command:   "cat",
			TimeoutMS: 2000,
		},
	}
	emitter, err := internalevents.NewEmitterFromConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := emitter.(*pkgevents.AsyncEmitter); !ok {
		t.Fatalf("expected *pkgevents.AsyncEmitter for exec sink, got %T", emitter)
	}
	_ = emitter.Close()
}

func TestNewEmitterFromConfigInvalidSink(t *testing.T) {
	cfg := &config.EventsConfig{
		Enabled: true,
		Sink:    "unknown-sink",
	}
	_, err := internalevents.NewEmitterFromConfig(cfg)
	if err == nil {
		t.Fatalf("expected error for unsupported sink name, got nil")
	}
}
