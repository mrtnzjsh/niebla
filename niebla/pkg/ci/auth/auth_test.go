package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestAuthTransport_Bearer(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHdr := r.Header.Get("Authorization")
		if authHdr != "Bearer secret-token-123" {
			t.Errorf("expected 'Bearer secret-token-123', got %q", authHdr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client, err := NewClient(0, AuthConfig{
		Mode:  "bearer",
		Token: "secret-token-123",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}

func TestAuthTransport_Token(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHdr := r.Header.Get("Authorization")
		if authHdr != "token forgejo-pat-456" {
			t.Errorf("expected 'token forgejo-pat-456', got %q", authHdr)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client, err := NewClient(0, AuthConfig{
		Mode:  "token",
		Token: "forgejo-pat-456",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}

func TestAuthTransport_Basic(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "ci-user" || pass != "ci-pass" {
			t.Errorf("expected user 'ci-user' and pass 'ci-pass', got %q, %q (ok=%v)", user, pass, ok)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client, err := NewClient(0, AuthConfig{
		Mode:     "basic",
		Username: "ci-user",
		Password: "ci-pass",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}

func TestAuthTransport_CustomHeader(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdrVal := r.Header.Get("X-Custom-Key")
		if hdrVal != "custom-secret-789" {
			t.Errorf("expected 'custom-secret-789', got %q", hdrVal)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client, err := NewClient(0, AuthConfig{
		Mode:       "header",
		HeaderName: "X-Custom-Key",
		Token:      "custom-secret-789",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("client.Get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}

func TestAuthTransport_HMAC(t *testing.T) {
	secret := []byte("top-secret-hmac-key")
	bodyContent := []byte(`{"event": "cl.created", "cl_id": 42}`)

	// Expected signature
	mac := hmac.New(sha256.New, secret)
	mac.Write(bodyContent)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sigHdr := r.Header.Get("X-Critique-Signature-256")
		if sigHdr != expectedSig {
			t.Errorf("expected signature %q, got %q", expectedSig, sigHdr)
			http.Error(w, "bad signature", http.StatusForbidden)
			return
		}
		receivedBody, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(receivedBody, bodyContent) {
			t.Errorf("expected received body %s, got %s (err: %v)", bodyContent, receivedBody, err)
			http.Error(w, "corrupt body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	client, err := NewClient(0, AuthConfig{
		Mode:   "hmac",
		Secret: string(secret),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := client.Post(ts.URL, "application/json", bytes.NewReader(bodyContent))
	if err != nil {
		t.Fatalf("client.Post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}

func TestAuthTransport_EnvResolution(t *testing.T) {
	os.Setenv("TEST_CI_TOKEN_VAR", "env-resolved-token")
	defer os.Unsetenv("TEST_CI_TOKEN_VAR")

	cfg := AuthConfig{
		Mode:     "bearer",
		TokenEnv: "TEST_CI_TOKEN_VAR",
	}
	if tok := cfg.ResolveToken(); tok != "env-resolved-token" {
		t.Fatalf("expected 'env-resolved-token', got %q", tok)
	}
}
