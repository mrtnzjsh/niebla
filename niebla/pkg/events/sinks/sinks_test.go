package sinks_test

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
	"time"

	"niebla/pkg/events"
	"niebla/pkg/events/sinks"
)

func TestNoopSink(t *testing.T) {
	sink := sinks.NewNoopSink()
	if sink.Name() != "noop" {
		t.Fatalf("expected name noop, got %s", sink.Name())
	}
	evt := events.New(events.TypeCLSubmitted, "critique", "1", nil)
	if err := sink.Send(context.Background(), evt); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("unexpected error on Close: %v", err)
	}
}

func TestWebhookSinkSuccessAndSignature(t *testing.T) {
	secret := "test-secret-key-123"
	var receivedBody []byte
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink, err := sinks.NewWebhookSink(sinks.WebhookConfig{
		Endpoint: server.URL,
		Secret:   secret,
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create webhook sink: %v", err)
	}
	if sink.Name() != "webhook" {
		t.Fatalf("expected name webhook, got %s", sink.Name())
	}

	payload := events.CLSubmittedPayload{
		CLID:   "101",
		Author: "alice",
	}
	evt := events.New(events.TypeCLSubmitted, "critique", "101", payload)

	if err := sink.Send(context.Background(), evt); err != nil {
		t.Fatalf("failed to send event via webhook: %v", err)
	}

	// Verify headers
	if got := receivedHeaders.Get("X-Critique-Event"); got != events.TypeCLSubmitted.String() {
		t.Fatalf("expected X-Critique-Event %s, got %s", events.TypeCLSubmitted, got)
	}
	if got := receivedHeaders.Get("X-Critique-Delivery"); got != evt.ID {
		t.Fatalf("expected X-Critique-Delivery %s, got %s", evt.ID, got)
	}

	// Verify HMAC-SHA256 signature
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(receivedBody)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := receivedHeaders.Get("X-Critique-Signature-256"); got != expectedSig {
		t.Fatalf("expected signature %s, got %s", expectedSig, got)
	}

	// Verify payload body
	var receivedEvt events.Event
	if err := json.Unmarshal(receivedBody, &receivedEvt); err != nil {
		t.Fatalf("failed to unmarshal received body: %v", err)
	}
	if receivedEvt.ID != evt.ID || receivedEvt.Type != evt.Type {
		t.Fatalf("unmarshaled event mismatch: %+v vs %+v", receivedEvt, evt)
	}
}

func TestWebhookSinkRetryOnServerError(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := attempts.Add(1)
		if att < 3 {
			w.WriteHeader(http.StatusServiceUnavailable) // Retryable
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink, err := sinks.NewWebhookSink(sinks.WebhookConfig{
		Endpoint:   server.URL,
		MaxRetries: 3,
		Timeout:    1 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create webhook sink: %v", err)
	}

	evt := events.New(events.TypeCLSubmitted, "critique", "101", nil)
	if err := sink.Send(context.Background(), evt); err != nil {
		t.Fatalf("expected webhook to succeed after retries, got err: %v", err)
	}

	if attempts.Load() != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", attempts.Load())
	}
}

func TestExecSink(t *testing.T) {
	sink, err := sinks.NewExecSink(sinks.ExecConfig{
		Command: "cat", // standard POSIX command that reads stdin
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("failed to create exec sink: %v", err)
	}
	if sink.Name() != "exec" {
		t.Fatalf("expected name exec, got %s", sink.Name())
	}

	evt := events.New(events.TypeCLSubmitted, "critique", "101", nil)
	if err := sink.Send(context.Background(), evt); err != nil {
		t.Fatalf("exec sink failed to execute cat: %v", err)
	}
}
