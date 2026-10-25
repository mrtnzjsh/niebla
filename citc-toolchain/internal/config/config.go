package config

import "strings"

// Config represents the unified toolchain configuration stored in ~/.config/citc-toolchain/config.toml.
type Config struct {
	Version  int            `toml:"version"`
	Core     CoreConfig     `toml:"core"`
	Server   ServerConfig   `toml:"server"`
	FS       FSConfig       `toml:"fs"`
	Critique CritiqueConfig `toml:"critique"`
}

// CoreConfig holds settings shared across toolchain components.
type CoreConfig struct {
	ServerAddr       string `toml:"server_addr"`
	DefaultWorkspace string `toml:"default_workspace"`
}

// ServerConfig holds daemon settings for citc-server.
type ServerConfig struct {
	Host      string `toml:"host"`
	Port      int    `toml:"port"`
	DBURL     string `toml:"db_url"`
	BlobPath  string `toml:"blob_path"`
	CacheSize int    `toml:"cache_size"`
}

// FSConfig holds filesystem mount and caching options for citcfs.
type FSConfig struct {
	MountBase string `toml:"mount_base"`
	CacheDir  string `toml:"cache_dir"`
}

// CritiqueAuthConfig governs user identity resolution in Critique.
type CritiqueAuthConfig struct {
	Mode        string `toml:"mode"`         // "local", "headers", "tailscale"
	DefaultUser string `toml:"default_user"` // defaults to "$USER"
	HeaderName  string `toml:"header_name"`  // defaults to "X-Forwarded-User"
}

// CritiqueGroupConfig governs enterprise user group resolution.
type CritiqueGroupConfig struct {
	Mode   string              `toml:"mode"`             // "static", "api", "disabled"
	Static map[string][]string `toml:"static,omitempty"` // map of group names to member usernames
	API    CritiqueGroupAPI    `toml:"api,omitempty"`
}

// CritiqueGroupAPI specifies settings for an external directory/IdP HTTP API.
type CritiqueGroupAPI struct {
	URL       string `toml:"url,omitempty"`
	TokenEnv  string `toml:"token_env,omitempty"`
	TimeoutMS int    `toml:"timeout_ms,omitempty"`
}

// CritiqueConfig holds review web service options.
type CritiqueConfig struct {
	Host                              string              `toml:"host"`
	Port                              int                 `toml:"port"`
	ProxyPort                         int                 `toml:"proxy_port"`
	DefaultReviewer                   string              `toml:"default_reviewer"`
	RequiredApprovals                 *int                `toml:"required_approvals,omitempty"`
	AllowSubmitWithUnresolvedComments *bool               `toml:"allow_submit_with_unresolved_comments,omitempty"`
	Auth                              CritiqueAuthConfig `toml:"auth"`
	Groups                            CritiqueGroupConfig `toml:"groups"`
	Theme                             ThemeConfig         `toml:"theme"`
}

// GetRequiredApprovals returns the configured required approvals count,
// or intelligently defaults to 0 in local/tailscale mode and 1 in multi-user/remote modes.
func (c *CritiqueConfig) GetRequiredApprovals() int {
	if c.RequiredApprovals != nil {
		return *c.RequiredApprovals
	}
	mode := strings.ToLower(strings.TrimSpace(c.Auth.Mode))
	if mode == "local" || mode == "tailscale" || mode == "" {
		return 0
	}
	return 1
}

// GetAllowSubmitWithUnresolvedComments returns whether CLs can be submitted with unresolved comments.
// Defaults strictly to false.
func (c *CritiqueConfig) GetAllowSubmitWithUnresolvedComments() bool {
	if c.AllowSubmitWithUnresolvedComments != nil {
		return *c.AllowSubmitWithUnresolvedComments
	}
	return false
}

// ThemeConfig governs the UI visual appearance and Chroma syntax highlighting.
type ThemeConfig struct {
	Preset      string       `toml:"preset"`
	SyntaxStyle string       `toml:"syntax_style"`
	Colors      ColorsConfig `toml:"colors"`
}

// ColorsConfig specifies custom hex codes or overrides for UI elements.
type ColorsConfig struct {
	BGCanvas       string `toml:"bg_canvas,omitempty"`
	BGSurface      string `toml:"bg_surface,omitempty"`
	BGCard         string `toml:"bg_card,omitempty"`
	Border         string `toml:"border,omitempty"`
	TextPrimary    string `toml:"text_primary,omitempty"`
	TextMuted      string `toml:"text_muted,omitempty"`
	Accent         string `toml:"accent,omitempty"`
	DiffAddBG      string `toml:"diff_add_bg,omitempty"`
	DiffAddBorder  string `toml:"diff_add_border,omitempty"`
	DiffDelBG      string `toml:"diff_del_bg,omitempty"`
	DiffDelBorder  string `toml:"diff_del_border,omitempty"`
	DiffModLeftBG  string `toml:"diff_mod_left_bg,omitempty"`
	DiffModRightBG string `toml:"diff_mod_right_bg,omitempty"`
}

// DefaultConfig returns a fresh Config struct populated with sensible default values.
func DefaultConfig() *Config {
	return &Config{
		Version: 1,
		Core: CoreConfig{
			ServerAddr:       "127.0.0.1:50051",
			DefaultWorkspace: "default",
		},
		Server: ServerConfig{
			Host:      "0.0.0.0",
			Port:      50051,
			DBURL:     "postgres:///citc?host=/run/postgresql",
			BlobPath:  "/devpool/citc_blobs",
			CacheSize: 10000,
		},
		FS: FSConfig{
			MountBase: "/citc",
			CacheDir:  "$HOME/.cache/citc/blobs",
		},
		Critique: CritiqueConfig{
			Host:            "0.0.0.0",
			Port:            8080,
			ProxyPort:       7331,
			DefaultReviewer: "$USER",
			Auth: CritiqueAuthConfig{
				Mode:        "local",
				DefaultUser: "$USER",
				HeaderName:  "X-Forwarded-User",
			},
			Groups: CritiqueGroupConfig{
				Mode:   "static",
				Static: make(map[string][]string),
			},
			Theme: ThemeConfig{
				Preset:      "catppuccin-mocha",
				SyntaxStyle: "catppuccin-mocha",
			},
		},
	}
}
