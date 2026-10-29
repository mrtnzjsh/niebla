package ci

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

type mockProvider struct {
	name        string
	triggerErr  error
	externalURL string
	triggered   bool
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) Trigger(ctx context.Context, req DispatchRequest) (string, error) {
	m.triggered = true
	return m.externalURL, m.triggerErr
}

type mockReporter struct {
	reports []CheckRunReport
}

func (m *mockReporter) ReportStatus(ctx context.Context, report CheckRunReport) error {
	m.reports = append(m.reports, report)
	return nil
}

func TestDispatcherAndReporter(t *testing.T) {
	reporter := &mockReporter{}
	dispatcher := NewDispatcher(reporter, "http://localhost:8080", "runner-token-xyz")

	localProv := &mockProvider{name: "local", externalURL: "http://ci.local/build/1"}
	dispatcher.RegisterProvider("local", localProv)

	req := DispatchRequest{
		CLID:     42,
		Patchset: 1,
		TreeHash: "abcd",
		Author:   "matatan",
	}

	// 1. Dispatch to registered provider -> succeeds
	err := dispatcher.Dispatch(context.Background(), "local", req)
	if err != nil {
		t.Fatalf("expected dispatch to succeed, got: %v", err)
	}
	if !localProv.triggered {
		t.Fatalf("expected provider to be triggered")
	}

	// Verify reporter recorded 'queued'
	if len(reporter.reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reporter.reports))
	}
	if reporter.reports[0].Status != "queued" || reporter.reports[0].CLID != 42 {
		t.Errorf("unexpected report: %+v", reporter.reports[0])
	}

	// 2. Dispatch to unknown provider -> fails
	err = dispatcher.Dispatch(context.Background(), "unknown-runner", req)
	if err == nil {
		t.Fatalf("expected unknown runner to fail")
	}

	// 3. Dispatch to failing provider -> reports failure
	failingProv := &mockProvider{
		name:        "failing",
		triggerErr:  fmt.Errorf("connection refused"),
		externalURL: "http://failing.ci/error",
	}
	dispatcher.RegisterProvider("failing", failingProv)

	err = dispatcher.Dispatch(context.Background(), "failing", req)
	if err == nil {
		t.Fatalf("expected failing runner to return error")
	}

	// Reporter should now have queued + failed reports
	if len(reporter.reports) != 3 {
		t.Fatalf("expected 3 total reports, got %d", len(reporter.reports))
	}
	lastReport := reporter.reports[2]
	if lastReport.Status != "failed" || lastReport.ExternalURL != "http://failing.ci/error" {
		t.Errorf("unexpected failed report: %+v", lastReport)
	}
}

func TestHTTPStatusReporter(t *testing.T) {
	var receivedReport CheckRunReport
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ci/status" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&receivedReport)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	reporter := NewHTTPStatusReporter(ts.URL, nil)
	err := reporter.ReportStatus(context.Background(), CheckRunReport{
		CLID:           10,
		PatchsetNumber: 2,
		RunnerName:     "local",
		Provider:       "local",
		Status:         "passed",
	})
	if err != nil {
		t.Fatalf("ReportStatus: %v", err)
	}

	if receivedReport.CLID != 10 || receivedReport.PatchsetNumber != 2 || receivedReport.Status != "passed" {
		t.Errorf("unexpected received report: %+v", receivedReport)
	}
}
