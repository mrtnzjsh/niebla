package sinks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"niebla/pkg/events"
)

// WebhookConfig defines connection and delivery options for WebhookSink.
type WebhookConfig struct {
	Endpoint   string
	Secret     string
	Timeout    time.Duration
	MaxRetries int
	Client     *http.Client
}

// WebhookSink transmits CloudEvents to an external HTTP endpoint.
type WebhookSink struct {
	endpoint   string
	secret     string
	timeout    time.Duration
	maxRetries int
	client     *http.Client
}

// NewWebhookSink creates an initialized WebhookSink.
func NewWebhookSink(cfg WebhookConfig) (*WebhookSink, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("webhook endpoint cannot be empty")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	client := cfg.Client
	if client == nil {
		client = &http.Client{
			Timeout: timeout,
		}
	}
	maxRetries := cfg.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}

	return &WebhookSink{
		endpoint:   cfg.Endpoint,
		secret:     cfg.Secret,
		timeout:    timeout,
		maxRetries: maxRetries,
		client:     client,
	}, nil
}

func (s *WebhookSink) Name() string {
	return "webhook"
}

// Send transmits an event via HTTP POST with HMAC signing and exponential retry.
func (s *WebhookSink) Send(ctx context.Context, evt events.Event) error {
	payload, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("failed to serialize event %s: %w", evt.ID, err)
	}

	var lastErr error
	backoff := 50 * time.Millisecond

	for attempt := 0; attempt <= s.maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
				if backoff > 2*time.Second {
					backoff = 2 * time.Second
				}
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("failed to construct webhook request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("User-Agent", "Critique-Notifier/1.0")
		req.Header.Set("X-Critique-Event", evt.Type)
		req.Header.Set("X-Critique-Delivery", evt.ID)

		if s.secret != "" {
			mac := hmac.New(sha256.New, []byte(s.secret))
			mac.Write(payload)
			sig := hex.EncodeToString(mac.Sum(nil))
			req.Header.Set("X-Critique-Signature-256", "sha256="+sig)
		}

		resp, err := s.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		_ = resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}

		lastErr = fmt.Errorf("webhook responded with status: %d", resp.StatusCode)

		// Do not retry 4xx client errors (bad requests, unauthorized, etc.)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return lastErr
		}
	}

	return fmt.Errorf("webhook failed after %d retries: %w", s.maxRetries, lastErr)
}

func (s *WebhookSink) Close() error {
	return nil
}
