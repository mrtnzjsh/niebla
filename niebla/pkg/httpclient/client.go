package httpclient

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// TLSOptions defines client-side TLS configuration (custom CA certificate pool and SNI server name).
type TLSOptions struct {
	Enabled    bool   `json:"enabled"`
	CAFile     string `json:"ca_file,omitempty"`
	ServerName string `json:"server_name,omitempty"`
}

// BuildTLSConfig constructs a *tls.Config enforcing TLS 1.3 and loading an optional PEM CA bundle.
func BuildTLSConfig(opts TLSOptions) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: opts.ServerName,
	}

	if opts.CAFile != "" {
		caPEM, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate from %s: %w", opts.CAFile, err)
		}
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("failed to parse CA certificate from %s", opts.CAFile)
		}
		tlsConfig.RootCAs = certPool
	}

	return tlsConfig, nil
}

// AuthConfig defines the authentication and signing credentials for outbound HTTP requests.
type AuthConfig struct {
	Mode        string `json:"mode"` // "bearer", "token", "basic", "header", "hmac", or "none"
	Token       string `json:"token,omitempty"`
	TokenEnv    string `json:"token_env,omitempty"`
	KeyFile     string `json:"key_file,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"password,omitempty"`
	PasswordEnv string `json:"password_env,omitempty"`
	HeaderName  string `json:"header_name,omitempty"`
	Secret      string `json:"secret,omitempty"`
	SecretEnv   string `json:"secret_env,omitempty"`
}

// ResolveToken returns the API or bearer token from Token, TokenEnv, or KeyFile.
func (a *AuthConfig) ResolveToken() string {
	if a.Token != "" {
		return a.Token
	}
	if a.TokenEnv != "" {
		if val := os.Getenv(a.TokenEnv); val != "" {
			return val
		}
	}
	if a.KeyFile != "" {
		if data, err := os.ReadFile(a.KeyFile); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

// ResolveSecret returns the secret key bytes for HMAC signing from Secret or SecretEnv.
func (a *AuthConfig) ResolveSecret() []byte {
	if a.Secret != "" {
		return []byte(a.Secret)
	}
	if a.SecretEnv != "" {
		if val := os.Getenv(a.SecretEnv); val != "" {
			return []byte(val)
		}
	}
	return nil
}

// ResolvePassword returns the basic auth password from Password or PasswordEnv.
func (a *AuthConfig) ResolvePassword() string {
	if a.Password != "" {
		return a.Password
	}
	if a.PasswordEnv != "" {
		if val := os.Getenv(a.PasswordEnv); val != "" {
			return val
		}
	}
	return ""
}

// authTransport decorates an underlying http.RoundTripper with authentication headers.
type authTransport struct {
	base http.RoundTripper
	cfg  AuthConfig
}

// NewAuthTransport returns an http.RoundTripper that injects authentication credentials.
func NewAuthTransport(base http.RoundTripper, cfg AuthConfig) (http.RoundTripper, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" {
		mode = "none"
	}
	cfg.Mode = mode

	validModes := map[string]bool{
		"none": true, "bearer": true, "token": true, "basic": true, "header": true, "hmac": true,
	}
	if !validModes[mode] {
		return nil, fmt.Errorf("unsupported auth mode: %q", cfg.Mode)
	}

	return &authTransport{
		base: base,
		cfg:  cfg,
	}, nil
}

// RoundTrip injects the configured authentication headers into req before delegating.
func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clonedReq := req.Clone(req.Context())

	switch t.cfg.Mode {
	case "bearer":
		token := t.cfg.ResolveToken()
		if token != "" {
			clonedReq.Header.Set("Authorization", "Bearer "+token)
		}
	case "token":
		token := t.cfg.ResolveToken()
		if token != "" {
			clonedReq.Header.Set("Authorization", "token "+token)
		}
	case "basic":
		password := t.cfg.ResolvePassword()
		clonedReq.SetBasicAuth(t.cfg.Username, password)
	case "header":
		token := t.cfg.ResolveToken()
		hdr := t.cfg.HeaderName
		if hdr == "" {
			hdr = "X-Api-Key"
		}
		if token != "" {
			clonedReq.Header.Set(hdr, token)
		}
	case "hmac":
		secret := t.cfg.ResolveSecret()
		if len(secret) > 0 {
			var bodyBytes []byte
			if req.Body != nil {
				var err error
				bodyBytes, err = io.ReadAll(req.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read body for HMAC: %w", err)
				}
				_ = req.Body.Close()
				clonedReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			}
			mac := hmac.New(sha256.New, secret)
			mac.Write(bodyBytes)
			sig := hex.EncodeToString(mac.Sum(nil))

			hdr := t.cfg.HeaderName
			if hdr == "" {
				hdr = "X-Critique-Signature-256"
			}
			clonedReq.Header.Set(hdr, "sha256="+sig)
		}
	case "none":
		// no-op
	}

	return t.base.RoundTrip(clonedReq)
}

// ClientOptions defines configuration for constructing a unified *http.Client.
type ClientOptions struct {
	Timeout       time.Duration
	Auth          AuthConfig
	TLS           TLSOptions
	BaseTransport http.RoundTripper
}

// New constructs an *http.Client configured with optional TLS root CA settings,
// authentication RoundTripper, and request timeout.
func New(opts ClientOptions) (*http.Client, error) {
	base := opts.BaseTransport
	if base == nil {
		if opts.TLS.Enabled || opts.TLS.CAFile != "" || opts.TLS.ServerName != "" {
			tlsCfg, err := BuildTLSConfig(opts.TLS)
			if err != nil {
				return nil, err
			}
			if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
				tr := defaultTransport.Clone()
				tr.TLSClientConfig = tlsCfg
				base = tr
			} else {
				base = &http.Transport{
					TLSClientConfig: tlsCfg,
				}
			}
		} else {
			base = http.DefaultTransport
		}
	}

	transport, err := NewAuthTransport(base, opts.Auth)
	if err != nil {
		return nil, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}, nil
}

// NewClient is a convenience constructor for an *http.Client with timeout and AuthConfig.
func NewClient(timeout time.Duration, cfg AuthConfig) (*http.Client, error) {
	return New(ClientOptions{
		Timeout: timeout,
		Auth:    cfg,
	})
}

