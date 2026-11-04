package webhook

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"niebla/pkg/ci"
	"niebla/pkg/ci/auth"
)

// Config specifies the settings for the generic webhook CI runner.
type Config struct {
	Name       string
	Endpoint   string // e.g. https://ci.internal.corp/hooks/niebla
	EventType  string // default: "niebla.cl.presubmit"
	Auth       auth.AuthConfig
	Timeout    time.Duration
	MaxRetries int
}

// Provider implements ci.Provider for generic HTTP webhooks.
type Provider struct {
	cfg    Config
	client *http.Client
}

// NewProvider creates a new webhook runner provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = "webhook"
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("endpoint cannot be empty for webhook runner %q", cfg.Name)
	}
	if cfg.EventType == "" {
		cfg.EventType = "niebla.cl.presubmit"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 2
	}

	client, err := auth.NewClient(cfg.Timeout, cfg.Auth)
	if err != nil {
		return nil, fmt.Errorf("failed to create webhook auth client: %w", err)
	}

	return &Provider{
		cfg:    cfg,
		client: client,
	}, nil
}

func (p *Provider) Name() string {
	return p.cfg.Name
}

type cloudEventPayload struct {
	SpecVersion     string      `json:"specversion"`
	ID              string      `json:"id"`
	Source          string      `json:"source"`
	Type            string      `json:"type"`
	DataContentType string      `json:"datacontenttype"`
	Time            string      `json:"time"`
	Data            interface{} `json:"data"`
}

func generateEventID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Trigger sends a CloudEvent payload to the configured endpoint with retry.
func (p *Provider) Trigger(ctx context.Context, req ci.DispatchRequest) (string, error) {
	event := cloudEventPayload{
		SpecVersion:     "1.0",
		ID:              generateEventID(),
		Source:          "niebla-ci",
		Type:            p.cfg.EventType,
		DataContentType: "application/json",
		Time:            time.Now().UTC().Format(time.RFC3339Nano),
		Data:            req,
	}

	bodyBytes, err := json.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("failed to encode cloudevent webhook payload: %w", err)
	}

	attempts := p.cfg.MaxRetries + 1
	var lastErr error

	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(1<<i) * 50 * time.Millisecond):
			}
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.Endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", fmt.Errorf("failed to create webhook request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/cloudevents+json")

		resp, err := p.client.Do(httpReq)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return p.cfg.Endpoint, nil
		}

		lastErr = fmt.Errorf("webhook received HTTP status %d", resp.StatusCode)
		if resp.StatusCode < 500 {
			// Client errors (4xx) should not be retried
			break
		}
	}

	return "", fmt.Errorf("webhook dispatch failed after %d attempts: %w", attempts, lastErr)
}
