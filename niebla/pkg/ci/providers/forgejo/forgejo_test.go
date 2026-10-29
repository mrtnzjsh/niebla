package forgejo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"niebla/pkg/ci"
	"niebla/pkg/ci/auth"
)

func TestForgejoProvider_Trigger(t *testing.T) {
	var receivedPayload dispatchPayload
	var receivedAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			http.Error(w, "invalid method", http.StatusMethodNotAllowed)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	prov, err := NewProvider(Config{
		Name:     "forgejo-linux",
		Endpoint: ts.URL,
		Ref:      "main",
		Auth: auth.AuthConfig{
			Mode:  "token",
			Token: "forgejo-secret-token",
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	url, err := prov.Trigger(context.Background(), ci.DispatchRequest{
		CLID:        42,
		Patchset:    2,
		TreeHash:    "tree-1234",
		Author:      "tester",
		ServerURL:   "https://niebla.corp",
		RunnerToken: "scoped-run-token",
	})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	if url != ts.URL {
		t.Errorf("expected return url %s, got %s", ts.URL, url)
	}
	if receivedAuth != "token forgejo-secret-token" {
		t.Errorf("expected 'token forgejo-secret-token', got %q", receivedAuth)
	}
	if receivedPayload.Ref != "main" {
		t.Errorf("expected ref 'main', got %q", receivedPayload.Ref)
	}
	if receivedPayload.Inputs["cl_id"] != "42" || receivedPayload.Inputs["patchset"] != "2" {
		t.Errorf("unexpected inputs: %+v", receivedPayload.Inputs)
	}
}
