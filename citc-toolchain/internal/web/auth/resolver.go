package auth

import (
	"net/http"
	"strings"

	"citc-toolchain/internal/config"
)

// UserIdentityResolver resolves the authenticated user identity for an incoming HTTP request.
type UserIdentityResolver interface {
	ResolveUser(r *http.Request) string
}

// NewUserIdentityResolver constructs an identity resolver according to the configuration mode.
func NewUserIdentityResolver(cfg config.CritiqueAuthConfig) UserIdentityResolver {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case "headers", "header":
		return &HeaderResolver{
			HeaderName:  cfg.HeaderName,
			DefaultUser: cfg.DefaultUser,
		}
	case "tailscale":
		return &TailscaleResolver{
			DefaultUser: cfg.DefaultUser,
		}
	case "local", "":
		fallthrough
	default:
		return &LocalResolver{
			DefaultUser: cfg.DefaultUser,
		}
	}
}

// LocalResolver resolves identity using the configured local user (defaulting to $USER).
// It also honors ?reviewer= query param for local dev and testing emulation.
type LocalResolver struct {
	DefaultUser string
}

func (l *LocalResolver) ResolveUser(r *http.Request) string {
	if r != nil {
		if q := r.URL.Query().Get("reviewer"); q != "" {
			return strings.TrimSpace(q)
		}
	}
	if l.DefaultUser != "" {
		return l.DefaultUser
	}
	return "reviewer"
}

// HeaderResolver resolves identity from a trusted ingress reverse-proxy header
// (such as X-Forwarded-User, X-Forwarded-Email, or Cf-Access-Authenticated-User-Email).
type HeaderResolver struct {
	HeaderName  string
	DefaultUser string
}

func (h *HeaderResolver) ResolveUser(r *http.Request) string {
	if r != nil {
		header := h.HeaderName
		if header == "" {
			header = "X-Forwarded-User"
		}
		if val := r.Header.Get(header); val != "" {
			return strings.TrimSpace(val)
		}
		// Also support query param override in development/test
		if q := r.URL.Query().Get("reviewer"); q != "" {
			return strings.TrimSpace(q)
		}
	}
	if h.DefaultUser != "" {
		return h.DefaultUser
	}
	return "reviewer"
}

// TailscaleResolver resolves identity via Tailscale Whois or falls back to default user.
type TailscaleResolver struct {
	DefaultUser string
}

func (t *TailscaleResolver) ResolveUser(r *http.Request) string {
	if r != nil {
		if q := r.URL.Query().Get("reviewer"); q != "" {
			return strings.TrimSpace(q)
		}
		if tsUser := r.Header.Get("Tailscale-User-Login"); tsUser != "" {
			return strings.TrimSpace(tsUser)
		}
	}
	if t.DefaultUser != "" {
		return t.DefaultUser
	}
	return "reviewer"
}
