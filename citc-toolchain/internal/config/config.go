package config

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

// CritiqueConfig holds review web service options.
type CritiqueConfig struct {
	Host            string             `toml:"host"`
	Port            int                `toml:"port"`
	ProxyPort       int                `toml:"proxy_port"`
	DefaultReviewer string             `toml:"default_reviewer"`
	Auth            CritiqueAuthConfig `toml:"auth"`
	Theme           ThemeConfig        `toml:"theme"`
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
			Theme: ThemeConfig{
				Preset:      "catppuccin-mocha",
				SyntaxStyle: "catppuccin-mocha",
			},
		},
	}
}
