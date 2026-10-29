package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"niebla/pkg/ci"
	"niebla/pkg/ci/auth"
)

func TestWebhookProvider_TriggerWithHMAC(t *testing.T) {
	secret := []byte("webhook-secret-key-999")
	var receivedEvent cloudEventPayload
	var receivedSig string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Critique-Signature-256")
		bodyBytes, _ := io.ReadAll(r.Body)

		// Verify signature
		mac := hmac.New(sha256.New, secret)
		mac.Write(bodyBytes)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if receivedSig != expected {
			http.Error(w, "invalid signature", http.StatusForbidden)
			return
		}

		_ = json.Unmarshal(bodyBytes, &receivedEvent)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	prov, err := NewProvider(Config{
		Name:     "custom-webhook",
		Endpoint: ts.URL,
		Auth: auth.AuthConfig{
			Mode:   "hmac",
			Secret: string(secret),
		},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	url, err := prov.Trigger(context.Background(), ci.DispatchRequest{
		CLID:     77,
		Patchset: 1,
		TreeHash: "hash77",
	})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if url != ts.URL {
		t.Errorf("expected return url %s, got %s", ts.URL, url)
	}
	if receivedEvent.Type != "niebla.cl.presubmit" {
		t.Errorf("expected type 'niebla.cl.presubmit', got %q", receivedEvent.Type)
	}
}

func TestWebhookProvider_RetryOn500(t *testing.T) {
	var attempts int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)
		if count < 2 {
			http.Error(w, "server busy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	prov, err := NewProvider(Config{
		Name:       "retry-webhook",
		Endpoint:   ts.URL,
		MaxRetries: 2,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	_, err = prov.Trigger(context.Background(), ci.DispatchRequest{
		CLID: 88,
	})
	if err != nil {
		t.Fatalf("expected trigger to succeed after retry, got: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}
