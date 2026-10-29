package auth

import (
	"net/http"
	"os"
	"os/user"
	"strings"

	"niebla/internal/config"
)

// CurrentOSUser returns the active OS username ($USER), falling back to user.Current() or "reviewer".
func CurrentOSUser() string {
	if u := strings.TrimSpace(os.Getenv("USER")); u != "" {
		return u
	}
	if cur, err := user.Current(); err == nil && cur.Username != "" {
		return cur.Username
	}
	return "reviewer"
}

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
	if l.DefaultUser != "" && l.DefaultUser != "$USER" {
		return l.DefaultUser
	}
	return CurrentOSUser()
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
	if h.DefaultUser != "" && h.DefaultUser != "$USER" {
		return h.DefaultUser
	}
	return CurrentOSUser()
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
	if t.DefaultUser != "" && t.DefaultUser != "$USER" {
		return t.DefaultUser
	}
	return CurrentOSUser()
}
