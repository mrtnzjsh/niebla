package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"strings"
	"time"

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
	switch config.AuthMode(mode) {
	case config.AuthModeHeaders, "header":
		return &HeaderResolver{
			HeaderName:         cfg.HeaderName,
			DefaultUser:        cfg.DefaultUser,
			TrustedProxies:     cfg.TrustedProxies,
			AllowQueryOverride: cfg.AllowQueryOverride,
		}
	case config.AuthModeTailscale:
		return &TailscaleResolver{
			DefaultUser:        cfg.DefaultUser,
			TrustedProxies:     cfg.TrustedProxies,
			AllowQueryOverride: cfg.AllowQueryOverride,
		}
	case config.AuthModeLocal, "":
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

// tailscaleWhoIsResponse represents the JSON response from tailscaled's /localapi/v0/whois endpoint.
type tailscaleWhoIsResponse struct {
	Node *struct {
		Name         string `json:"Name"`
		ComputedName string `json:"ComputedName"`
	} `json:"Node"`
	UserProfile *struct {
		LoginName   string `json:"LoginName"`
		DisplayName string `json:"DisplayName"`
	} `json:"UserProfile"`
}

var defaultTailscaleSocketPaths = []string{
	"/run/tailscale/tailscaled.sock",
	"/var/run/tailscale/tailscaled.sock",
}

// queryTailscaleWhoIs queries the local tailscaled daemon socket to identify the remote peer.
func queryTailscaleWhoIs(ctx context.Context, remoteAddr string) (string, error) {
	var socketPath string
	for _, p := range defaultTailscaleSocketPaths {
		if _, err := os.Stat(p); err == nil {
			socketPath = p
			break
		}
	}
	if socketPath == "" {
		return "", fmt.Errorf("tailscaled socket not found")
	}

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 2 * time.Second,
	}

	reqURL := fmt.Sprintf("http://local-tailscaled.sock/localapi/v0/whois?addr=%s", url.QueryEscape(remoteAddr))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tailscaled whois returned status %d", resp.StatusCode)
	}

	var whois tailscaleWhoIsResponse
	if err := json.NewDecoder(resp.Body).Decode(&whois); err != nil {
		return "", err
	}

	if whois.UserProfile != nil && whois.UserProfile.LoginName != "" {
		return whois.UserProfile.LoginName, nil
	}
	if whois.Node != nil && whois.Node.ComputedName != "" {
		return whois.Node.ComputedName, nil
	}
	if whois.Node != nil && whois.Node.Name != "" {
		return whois.Node.Name, nil
	}

	return "", fmt.Errorf("no user or node identity in tailscale whois response")
}

// TailscaleResolver resolves identity via the Tailscale daemon WhoIs API or authenticated Tailscale Serve headers.
type TailscaleResolver struct {
	DefaultUser        string
	TrustedProxies     []string
	AllowQueryOverride bool
}

func (t *TailscaleResolver) ResolveUser(r *http.Request) string {
	if r != nil {
		if t.AllowQueryOverride {
			if q := r.URL.Query().Get("reviewer"); q != "" {
				return strings.TrimSpace(q)
			}
		}

		// 1. Authenticated Tailscale Serve header if request comes from loopback or trusted proxy
		if isTrustedRemoteAddr(r.RemoteAddr, t.TrustedProxies) {
			if tsUser := r.Header.Get("Tailscale-User-Login"); tsUser != "" {
				return strings.TrimSpace(tsUser)
			}
		}

		// 2. Direct Tailnet peer connection: query local tailscaled daemon WhoIs socket
		if r.RemoteAddr != "" {
			if user, err := queryTailscaleWhoIs(r.Context(), r.RemoteAddr); err == nil && user != "" {
				return user
			}
		}
	}

	if t.DefaultUser != "" && t.DefaultUser != "$USER" {
		return t.DefaultUser
	}
	return CurrentOSUser()
}
