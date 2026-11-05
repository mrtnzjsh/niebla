package auth

import (
	"net"
	"net/http"
	"os"
	"os/user"
	"strings"

	"tailscale.com/client/tailscale"

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

// isTrustedRemoteAddr asserts that remoteAddr originates from loopback or configured trusted proxy CIDRs/IPs.
func isTrustedRemoteAddr(remoteAddr string, trustedProxies []string) bool {
	if remoteAddr == "" {
		return false
	}
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	// If no custom proxies configured, default strictly to loopback (127.0.0.1, ::1)
	if len(trustedProxies) == 0 {
		return ip.IsLoopback()
	}
	for _, entry := range trustedProxies {
		entry = strings.TrimSpace(entry)
		if entry == "*" {
			return true
		}
		if strings.Contains(entry, "/") {
			if _, cidr, err := net.ParseCIDR(entry); err == nil && cidr.Contains(ip) {
				return true
			}
		} else if targetIP := net.ParseIP(entry); targetIP != nil && targetIP.Equal(ip) {
			return true
		}
	}
	return false
}

// NewUserIdentityResolver constructs an identity resolver according to the configuration mode.
func NewUserIdentityResolver(cfg config.CritiqueAuthConfig) UserIdentityResolver {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	switch mode {
	case "headers", "header":
		return &HeaderResolver{
			HeaderName:         cfg.HeaderName,
			DefaultUser:        cfg.DefaultUser,
			TrustedProxies:     cfg.TrustedProxies,
			AllowQueryOverride: cfg.AllowQueryOverride,
		}
	case "tailscale":
		return &TailscaleResolver{
			DefaultUser:        cfg.DefaultUser,
			TrustedProxies:     cfg.TrustedProxies,
			AllowQueryOverride: cfg.AllowQueryOverride,
			tsClient:           &tailscale.LocalClient{},
		}
	case "local", "":
		fallthrough
	default:
		return &LocalResolver{
			DefaultUser:          cfg.DefaultUser,
			DisableQueryOverride: false,
		}
	}
}

// LocalResolver resolves identity using the configured local user (defaulting to $USER).
// It also honors ?reviewer= query param for local dev and testing emulation unless DisableQueryOverride is true.
type LocalResolver struct {
	DefaultUser          string
	DisableQueryOverride bool
}

func (l *LocalResolver) ResolveUser(r *http.Request) string {
	if !l.DisableQueryOverride && r != nil {
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
// Headers are only trusted when requests originate from verified trusted proxy IPs.
type HeaderResolver struct {
	HeaderName         string
	DefaultUser        string
	TrustedProxies     []string
	AllowQueryOverride bool
}

func (h *HeaderResolver) ResolveUser(r *http.Request) string {
	if r != nil {
		if h.AllowQueryOverride {
			if q := r.URL.Query().Get("reviewer"); q != "" {
				return strings.TrimSpace(q)
			}
		}
		header := h.HeaderName
		if header == "" {
			header = "X-Forwarded-User"
		}
		// Security enforcement: only inspect header if request originates from trusted proxy
		if isTrustedRemoteAddr(r.RemoteAddr, h.TrustedProxies) {
			if val := r.Header.Get(header); val != "" {
				return strings.TrimSpace(val)
			}
		}
	}
	if h.DefaultUser != "" && h.DefaultUser != "$USER" {
		return h.DefaultUser
	}
	return CurrentOSUser()
}

// TailscaleResolver resolves identity via the Tailscale daemon WhoIs API or authenticated Tailscale Serve headers.
type TailscaleResolver struct {
	DefaultUser        string
	TrustedProxies     []string
	AllowQueryOverride bool
	tsClient           *tailscale.LocalClient
}

func (t *TailscaleResolver) ResolveUser(r *http.Request) string {
	if r != nil {
		if t.AllowQueryOverride {
			if q := r.URL.Query().Get("reviewer"); q != "" {
				return strings.TrimSpace(q)
			}
		}

		// 1. Check authenticated Tailscale Serve header if request comes from loopback or trusted proxy
		if isTrustedRemoteAddr(r.RemoteAddr, t.TrustedProxies) {
			if tsUser := r.Header.Get("Tailscale-User-Login"); tsUser != "" {
				return strings.TrimSpace(tsUser)
			}
		}

		// 2. Direct Tailnet peer connection: query local tailscaled daemon WhoIs
		if t.tsClient != nil && r.RemoteAddr != "" {
			if whois, err := t.tsClient.WhoIs(r.Context(), r.RemoteAddr); err == nil && whois != nil {
				if whois.UserProfile != nil && whois.UserProfile.LoginName != "" {
					return whois.UserProfile.LoginName
				}
				if whois.Node != nil && whois.Node.Name != "" {
					return whois.Node.Name
				}
			}
		}
	}

	if t.DefaultUser != "" && t.DefaultUser != "$USER" {
		return t.DefaultUser
	}
	return CurrentOSUser()
}
