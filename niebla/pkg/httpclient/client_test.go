package httpclient

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewClient_AuthModes(t *testing.T) {
	t.Run("bearer from env", func(t *testing.T) {
		t.Setenv("TEST_BEARER_TOKEN", "secret-token-123")

		var gotAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		c, err := New(ClientOptions{
			Timeout: 5 * time.Second,
			Auth: AuthConfig{
				Mode:     "bearer",
				TokenEnv: "TEST_BEARER_TOKEN",
			},
		})
		if err != nil {
			t.Fatalf("New failed: %v", err)
		}

		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		_ = resp.Body.Close()

		if gotAuth != "Bearer secret-token-123" {
			t.Errorf("expected 'Bearer secret-token-123', got %q", gotAuth)
		}
	})

	t.Run("hmac signing", func(t *testing.T) {
		var gotSig string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotSig = r.Header.Get("X-Critique-Signature-256")
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		c, err := NewClient(5*time.Second, AuthConfig{
			Mode:   "hmac",
			Secret: "my-webhook-secret",
		})
		if err != nil {
			t.Fatalf("NewClient failed: %v", err)
		}

		resp, err := c.Post(srv.URL, "application/json", strings.NewReader(`{"hello":"world"}`))
		if err != nil {
			t.Fatalf("Post failed: %v", err)
		}
		_ = resp.Body.Close()

		if !strings.HasPrefix(gotSig, "sha256=") {
			t.Errorf("expected sha256= signature prefix, got %q", gotSig)
		}
	})
}

func TestBuildTLSConfig_CustomCA(t *testing.T) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Niebla Test CA"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create cert: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	caPath := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caPath, pemBytes, 0600); err != nil {
		t.Fatalf("failed to write ca file: %v", err)
	}

	tlsCfg, err := BuildTLSConfig(TLSOptions{
		Enabled:    true,
		CAFile:     caPath,
		ServerName: "llm.internal.corp",
	})
	if err != nil {
		t.Fatalf("BuildTLSConfig failed: %v", err)
	}
	if tlsCfg.ServerName != "llm.internal.corp" {
		t.Errorf("expected ServerName 'llm.internal.corp', got %q", tlsCfg.ServerName)
	}
	if tlsCfg.RootCAs == nil {
		t.Errorf("expected non-nil RootCAs")
	}
}

