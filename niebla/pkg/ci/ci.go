package ci

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DispatchRequest contains the context needed by a runner to execute a build/test job on a CL patchset.
type DispatchRequest struct {
	CLID        int64  `json:"cl_id"`
	Patchset    int    `json:"patchset"`
	TreeHash    string `json:"tree_hash"`
	Author      string `json:"author"`
	ServerURL   string `json:"server_url"`
	RunnerToken string `json:"runner_token,omitempty"`
}

// CheckRunReport encapsulates the state of a check execution reported back to the VCS server.
type CheckRunReport struct {
	CLID           int64      `json:"cl_id"`
	PatchsetNumber int        `json:"patchset_number"`
	RunnerName     string     `json:"runner_name"`
	Provider       string     `json:"provider"`
	Status         string     `json:"status"` // queued, running, passed, failed, canceled, skipped
	ExternalURL    string     `json:"external_url,omitempty"`
	LogBlobSHA256  string     `json:"log_blob_sha256,omitempty"` // Hex-encoded 64-char sha256
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// Provider represents an execution driver capable of running tests against a CL patchset.
type Provider interface {
	Name() string
	Trigger(ctx context.Context, req DispatchRequest) (externalRunURL string, err error)
}

// StatusReporter abstracts reporting check status updates back to the VCS.
type StatusReporter interface {
	ReportStatus(ctx context.Context, report CheckRunReport) error
}

// HTTPStatusReporter sends check run updates to Cotejo / Niebla Server via REST API.
type HTTPStatusReporter struct {
	serverURL  string
	httpClient *http.Client
}

// NewHTTPStatusReporter creates a status reporter targeting serverURL.
func NewHTTPStatusReporter(serverURL string, client *http.Client) *HTTPStatusReporter {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	baseURL := strings.TrimRight(serverURL, "/")
	return &HTTPStatusReporter{
		serverURL:  baseURL,
		httpClient: client,
	}
}

// ReportStatus posts a check run update to /api/v1/ci/status.
func (r *HTTPStatusReporter) ReportStatus(ctx context.Context, report CheckRunReport) error {
	payloadBytes, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to encode report: %w", err)
	}

	endpoint := fmt.Sprintf("%s/api/v1/ci/status", r.serverURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("failed to create status request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send status report: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status reporter received HTTP %d from %s", resp.StatusCode, endpoint)
	}
	return nil
}

// Dispatcher orchestrates triggering configured CI providers and reporting initial status.
type Dispatcher struct {
	providers   map[string]Provider
	reporter    StatusReporter
	serverURL   string
	runnerToken string
}

// NewDispatcher creates a new orchestrator.
func NewDispatcher(reporter StatusReporter, serverURL, runnerToken string) *Dispatcher {
	return &Dispatcher{
		providers:   make(map[string]Provider),
		reporter:    reporter,
		serverURL:   serverURL,
		runnerToken: runnerToken,
	}
}

func (d *Dispatcher) RegisterProvider(runnerName string, provider Provider) {
	d.providers[runnerName] = provider
}

func (d *Dispatcher) GetProvider(runnerName string) (Provider, bool) {
	provider, ok := d.providers[runnerName]
	return provider, ok
}

func (d *Dispatcher) Dispatch(ctx context.Context, runnerName string, req DispatchRequest) error {
	provider, ok := d.providers[runnerName]
	if !ok {
		return fmt.Errorf("runner %q is not registered", runnerName)
	}

	if req.ServerURL == "" {
		req.ServerURL = d.serverURL
	}
	if req.RunnerToken == "" {
		req.RunnerToken = d.runnerToken
	}

	if d.reporter != nil {
		_ = d.reporter.ReportStatus(ctx, CheckRunReport{
			CLID:           req.CLID,
			PatchsetNumber: req.Patchset,
			RunnerName:     runnerName,
			Provider:       provider.Name(),
			Status:         "queued",
		})
	}

	externalURL, err := provider.Trigger(ctx, req)
	if err != nil {
		if d.reporter != nil {
			_ = d.reporter.ReportStatus(ctx, CheckRunReport{
				CLID:           req.CLID,
				PatchsetNumber: req.Patchset,
				RunnerName:     runnerName,
				Provider:       provider.Name(),
				Status:         "failed",
				ExternalURL:    externalURL,
			})
		}
		return fmt.Errorf("provider %q trigger failed: %w", runnerName, err)
	}

	return nil
}
