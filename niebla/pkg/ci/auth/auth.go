package auth

import (
	"net/http"
	"time"

	"niebla/pkg/httpclient"
)

// AuthConfig is an alias for httpclient.AuthConfig for backwards compatibility.
type AuthConfig = httpclient.AuthConfig

// NewAuthTransport returns an http.RoundTripper that injects authentication credentials.
func NewAuthTransport(base http.RoundTripper, cfg AuthConfig) (http.RoundTripper, error) {
	return httpclient.NewAuthTransport(base, cfg)
}

// NewClient returns an *http.Client configured with the given timeout and AuthConfig roundtripper.
func NewClient(timeout time.Duration, cfg AuthConfig) (*http.Client, error) {
	return httpclient.NewClient(timeout, cfg)
}
