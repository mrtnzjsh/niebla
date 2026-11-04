package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"niebla/pkg/ci"
	"niebla/pkg/ci/auth"
)

// Config specifies the configuration for GitHub Actions dispatch.
type Config struct {
	Name      string
	Endpoint  string // e.g. https://api.github.com/repos/{owner}/{repo}/dispatches
	EventType string // e.g. "niebla_presubmit" (for repository_dispatch)
	Ref       string // git ref (for workflow_dispatch, default "main")
	Auth      auth.AuthConfig
	Timeout   time.Duration
}

// Provider implements ci.Provider for GitHub Actions.
type Provider struct {
	cfg    Config
	client *http.Client
}

// NewProvider creates a new GitHub Actions dispatch provider.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Name == "" {
		cfg.Name = "github"
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("endpoint cannot be empty for github runner %q", cfg.Name)
	}
	if cfg.Ref == "" {
		cfg.Ref = "main"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}

	client, err := auth.NewClient(cfg.Timeout, cfg.Auth)
	if err != nil {
		return nil, fmt.Errorf("failed to create github auth client: %w", err)
	}

	return &Provider{
		cfg:    cfg,
		client: client,
	}, nil
}

func (p *Provider) Name() string {
	return p.cfg.Name
}

type repositoryDispatchPayload struct {
	EventType     string                 `json:"event_type"`
	ClientPayload map[string]interface{} `json:"client_payload"`
}

type workflowDispatchPayload struct {
	Ref    string            `json:"ref"`
	Inputs map[string]string `json:"inputs"`
}

// Trigger sends a dispatch request to the GitHub REST API.
func (p *Provider) Trigger(ctx context.Context, req ci.DispatchRequest) (string, error) {
	var bodyBytes []byte
	var err error

	if p.cfg.EventType != "" {
		// Repository Dispatch API
		payload := repositoryDispatchPayload{
			EventType: p.cfg.EventType,
			ClientPayload: map[string]interface{}{
				"cl_id":        req.CLID,
				"patchset":     req.Patchset,
				"tree_hash":    req.TreeHash,
				"author":       req.Author,
				"server_url":   req.ServerURL,
				"runner_token": req.RunnerToken,
			},
		}
		bodyBytes, err = json.Marshal(payload)
	} else {
		// Workflow Dispatch API
		payload := workflowDispatchPayload{
			Ref: p.cfg.Ref,
			Inputs: map[string]string{
				"cl_id":        strconv.FormatInt(req.CLID, 10),
				"patchset":     strconv.Itoa(req.Patchset),
				"tree_hash":    req.TreeHash,
				"author":       req.Author,
				"server_url":   req.ServerURL,
				"runner_token": req.RunnerToken,
			},
		}
		bodyBytes, err = json.Marshal(payload)
	}
	if err != nil {
		return "", fmt.Errorf("failed to encode github dispatch payload: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create github request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("github dispatch request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github dispatch failed with HTTP status %d", resp.StatusCode)
	}

	return p.cfg.Endpoint, nil
}
