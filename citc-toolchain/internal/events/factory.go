package events

import (
	"fmt"
	"log"
	"os"
	"time"

	"citc-toolchain/internal/config"
	pkgevents "citc-toolchain/pkg/events"
	"citc-toolchain/pkg/events/sinks"
)

// NewEmitterFromConfig builds a pkgevents.Emitter based on the toolchain EventsConfig.
// If events are disabled (the default for local use), it returns a zero-overhead NoopEmitter.
func NewEmitterFromConfig(cfg *config.EventsConfig) (pkgevents.Emitter, error) {
	if cfg == nil || !cfg.Enabled {
		return pkgevents.NewNoopEmitter(), nil
	}

	var sink pkgevents.Sink
	var err error

	switch cfg.Sink {
	case "webhook":
		timeout := time.Duration(cfg.Webhook.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = 3 * time.Second
		}
		secret := ""
		if cfg.Webhook.SecretTokenEnv != "" {
			secret = os.Getenv(cfg.Webhook.SecretTokenEnv)
		}
		sink, err = sinks.NewWebhookSink(sinks.WebhookConfig{
			Endpoint:   cfg.Webhook.Endpoint,
			Secret:     secret,
			Timeout:    timeout,
			MaxRetries: cfg.Webhook.MaxRetries,
		})
	case "exec":
		timeout := time.Duration(cfg.Exec.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		sink, err = sinks.NewExecSink(sinks.ExecConfig{
			Command: cfg.Exec.Command,
			Timeout: timeout,
		})
	case "noop", "":
		return pkgevents.NewNoopEmitter(), nil
	default:
		return nil, fmt.Errorf("unsupported events sink: %q (expected 'webhook', 'exec', or 'noop')", cfg.Sink)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to initialize %s event sink: %w", cfg.Sink, err)
	}

	bufferSize := cfg.BufferSize
	if bufferSize <= 0 {
		bufferSize = 256
	}

	opts := []pkgevents.AsyncOption{
		pkgevents.WithBufferSize(bufferSize),
		pkgevents.WithFilters(cfg.Filter),
		pkgevents.WithErrorHandler(func(err error, evt pkgevents.Event) {
			log.Printf("⚠️  [events] Failed to deliver event %s (%s) to %s sink: %v", evt.ID, evt.Type, sink.Name(), err)
		}),
	}

	return pkgevents.NewAsyncEmitter(sink, opts...), nil
}
