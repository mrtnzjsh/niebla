package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"niebla/pkg/ci"
	"niebla/pkg/ci/auth"
)

func TestGitHubProvider_RepositoryDispatch(t *testing.T) {
	var receivedPayload repositoryDispatchPayload
	var receivedAuth, receivedApiVer string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedApiVer = r.Header.Get("X-GitHub-Api-Version")
		if r.Method != http.MethodPost {
			http.Error(w, "invalid method", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	prov, err := NewProvider(Config{
		Name:      "github-presubmit",
		Endpoint:  ts.URL,
		EventType: "niebla_presubmit",
		Auth: auth.AuthConfig{
			Mode:  "bearer",
			Token: "ghp_secrettoken123",
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	url, err := prov.Trigger(context.Background(), ci.DispatchRequest{
		CLID:        100,
		Patchset:    3,
		TreeHash:    "deadbeef",
		Author:      "octocat",
		ServerURL:   "https://niebla.dev",
		RunnerToken: "ephemeral-token",
	})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	if url != ts.URL {
		t.Errorf("expected return url %s, got %s", ts.URL, url)
	}
	if receivedAuth != "Bearer ghp_secrettoken123" {
		t.Errorf("expected 'Bearer ghp_secrettoken123', got %q", receivedAuth)
	}
	if receivedApiVer != "2022-11-28" {
		t.Errorf("expected api version '2022-11-28', got %q", receivedApiVer)
	}
	if receivedPayload.EventType != "niebla_presubmit" {
		t.Errorf("expected event_type 'niebla_presubmit', got %q", receivedPayload.EventType)
	}
	if receivedPayload.ClientPayload["author"] != "octocat" {
		t.Errorf("unexpected client payload: %+v", receivedPayload.ClientPayload)
	}
}

func TestGitHubProvider_WorkflowDispatch(t *testing.T) {
	var receivedPayload workflowDispatchPayload

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	prov, err := NewProvider(Config{
		Name:     "github-workflow",
		Endpoint: ts.URL,
		Ref:      "release-v1",
		Auth: auth.AuthConfig{
			Mode:  "bearer",
			Token: "ghp_tok",
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	_, err = prov.Trigger(context.Background(), ci.DispatchRequest{
		CLID:     55,
		Patchset: 1,
	})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	if receivedPayload.Ref != "release-v1" {
		t.Errorf("expected ref 'release-v1', got %q", receivedPayload.Ref)
	}
	if receivedPayload.Inputs["cl_id"] != "55" {
		t.Errorf("expected cl_id '55', got %q", receivedPayload.Inputs["cl_id"])
	}
}
