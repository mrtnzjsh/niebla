package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Config represents the unified toolchain configuration stored in ~/.config/niebla/config.toml.
type Config struct {
	Version    int              `toml:"version"`
	Core       CoreConfig       `toml:"core"`
	Server     ServerConfig     `toml:"server"`
	FS         FSConfig         `toml:"fs"`
	Critique   CritiqueConfig   `toml:"critique"`
	Events     EventsConfig     `toml:"events"`
	Resilience ResilienceConfig `toml:"resilience"`
	CI         CIConfig         `toml:"ci"`
}

// CoreConfig holds settings shared across toolchain components.
type CoreConfig struct {
	ServerAddr       string          `toml:"server_addr"`
	DefaultWorkspace string          `toml:"default_workspace"`
	AuthToken        string          `toml:"auth_token,omitempty"`
	TLS              ClientTLSConfig `toml:"tls"`
}

// ClientTLSConfig holds client-side transport security options for connecting to niebla-server.
type ClientTLSConfig struct {
	Enabled    bool   `toml:"enabled"`
	CAFile     string `toml:"ca_file,omitempty"`
	ServerName string `toml:"server_name,omitempty"`
}

// ServerConfig holds daemon settings for niebla-server.
type ServerConfig struct {
	Host       string          `toml:"host"`
	Port       int             `toml:"port"`
	DBURL      string          `toml:"db_url"`
	BlobPath   string          `toml:"blob_path"`
	CacheSize  int             `toml:"cache_size"`
	AutoDB     *bool           `toml:"auto_db,omitempty"`
	AuthTokens []string        `toml:"auth_tokens,omitempty"`
	TLS        ServerTLSConfig `toml:"tls"`
}

// ShouldAutoDB returns true if local PostgreSQL auto-spawning is enabled (defaults to true).
func (s *ServerConfig) ShouldAutoDB() bool {
	if s != nil && s.AutoDB != nil {
		return *s.AutoDB
	}
	return true
}

// ServerTLSConfig holds TLS listener configuration for niebla-server.
type ServerTLSConfig struct {
	Enabled      bool   `toml:"enabled"`
	CertFile     string `toml:"cert_file,omitempty"`
	KeyFile      string `toml:"key_file,omitempty"`
	ClientCAFile string `toml:"client_ca_file,omitempty"`
}

// FSConfig holds filesystem mount and caching options for nieblafs.
type FSConfig struct {
	MountBase string `toml:"mount_base"`
	CacheDir  string `toml:"cache_dir"`
}

// CritiqueAuthConfig governs user identity resolution in Critique.
type CritiqueAuthConfig struct {
	Mode               string   `toml:"mode"`                 // "local", "headers", "tailscale"
	DefaultUser        string   `toml:"default_user"`         // defaults to "$USER"
	HeaderName         string   `toml:"header_name"`          // defaults to "X-Forwarded-User"
	TrustedProxies     []string `toml:"trusted_proxies"`      // list of trusted proxy CIDRs/IPs for header auth (defaults to loopback)
	AllowQueryOverride bool     `toml:"allow_query_override"` // allow ?reviewer= query param (dev/test only)
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

// EventsConfig governs event emission across the toolchain.
type EventsConfig struct {
	Enabled    bool              `toml:"enabled"`
	Sink       string            `toml:"sink"`                  // "webhook", "exec", "log", "noop"
	Filter     []string          `toml:"filter,omitempty"`      // event type globs e.g. "niebla.cl.*"
	BufferSize int               `toml:"buffer_size,omitempty"` // channel capacity (defaults to 256)
	Webhook    WebhookSinkConfig `toml:"webhook,omitempty"`
	Exec       ExecSinkConfig    `toml:"exec,omitempty"`
}

// WebhookSinkConfig specifies parameters for HTTP POST webhook event delivery.
type WebhookSinkConfig struct {
	Endpoint       string `toml:"endpoint"`
	SecretTokenEnv string `toml:"secret_token_env,omitempty"`
	TimeoutMS      int    `toml:"timeout_ms,omitempty"`
	MaxRetries     int    `toml:"max_retries,omitempty"`
}

// ExecSinkConfig specifies parameters for executing an external command with event on stdin.
type ExecSinkConfig struct {
	Command   string `toml:"command"`
	TimeoutMS int    `toml:"timeout_ms,omitempty"`
}

// ResilienceConfig governs distributed replication, encrypted backups, and disaster recovery.
type ResilienceConfig struct {
	Enabled             bool                      `toml:"enabled"`
	DefaultMode         string                    `toml:"default_mode"` // "zfs_stream" (default) or "bundle"
	ZFS                 ZFSReplicationConfig      `toml:"zfs,omitempty"`
	Bundle              BundleConfig              `toml:"bundle,omitempty"`
	Encryption          EncryptionConfig          `toml:"encryption,omitempty"`
	Filesystem          FilesystemBackupConfig    `toml:"filesystem,omitempty"`
	Nextcloud           NextcloudConfig           `toml:"nextcloud,omitempty"`
	SFTP                SFTPConfig                `toml:"sftp,omitempty"`
	S3                  S3BackupConfig            `toml:"s3,omitempty"`
	PostgresReplication PostgresReplicationConfig `toml:"postgres_replication,omitempty"`
	Schedule            BackupScheduleConfig      `toml:"schedule,omitempty"`
}

// ZFSReplicationConfig specifies parameters for native ZFS snapshot streaming over 10GbE.
type ZFSReplicationConfig struct {
	Enabled          bool   `toml:"enabled"`
	LocalDataset     string `toml:"local_dataset"`
	RemoteHost       string `toml:"remote_host"`
	RemoteUser       string `toml:"remote_user"`
	RemoteSSHPort    int    `toml:"remote_ssh_port,omitempty"`
	RemoteKeyFile    string `toml:"remote_key_file,omitempty"`
	RemoteDataset    string `toml:"remote_dataset"`
	RawEncryptedSend bool   `toml:"raw_encrypted_send,omitempty"`
	SnapshotPrefix   string `toml:"snapshot_prefix,omitempty"`
	RetentionCount   int    `toml:"retention_count,omitempty"`
	BandwidthLimitMB int    `toml:"bandwidth_limit_mb,omitempty"`
}

// BundleConfig specifies options for standalone encrypted archive packages (.nieblabak).
type BundleConfig struct {
	Enabled          bool   `toml:"enabled"`
	TargetDir        string `toml:"target_dir"`
	Compression      string `toml:"compression,omitempty"`       // "zstd" or "gzip"
	CompressionLevel int    `toml:"compression_level,omitempty"` // 1-9
}

// EncryptionConfig governs authenticated encryption at rest for backups.
type EncryptionConfig struct {
	Algorithm     string `toml:"algorithm"`                // "aes-256-gcm" or "chacha20-poly1305"
	KeySource     string `toml:"key_source"`               // "env", "file", "passphrase"
	KeyEnv        string `toml:"key_env,omitempty"`        // environment variable with master key
	KeyFile       string `toml:"key_file,omitempty"`       // path to 0400/0600 key file
	PassphraseEnv string `toml:"passphrase_env,omitempty"` // environment variable with passphrase
}

// FilesystemBackupConfig specifies high-speed filesystem destination (e.g. NFSv4/SMB3 10G mount).
type FilesystemBackupConfig struct {
	Enabled       bool   `toml:"enabled"`
	Path          string `toml:"path"`
	BufferSizeMB  int    `toml:"buffer_size_mb,omitempty"`
	AutoCreateDir bool   `toml:"auto_create_dir,omitempty"`
}

// NextcloudConfig specifies WebDAV backup destination for Nextcloud storage.
type NextcloudConfig struct {
	Enabled        bool   `toml:"enabled"`
	URL            string `toml:"url"`
	Username       string `toml:"username"`
	PasswordEnv    string `toml:"password_env"`
	TimeoutSeconds int    `toml:"timeout_seconds,omitempty"`
}

// SFTPConfig specifies remote SSH/SFTP streaming destination over 10GbE.
type SFTPConfig struct {
	Enabled        bool   `toml:"enabled"`
	Host           string `toml:"host"`
	User           string `toml:"user"`
	RemotePath     string `toml:"remote_path"`
	KeyFile        string `toml:"key_file,omitempty"`
	TimeoutSeconds int    `toml:"timeout_seconds,omitempty"`
}

// S3BackupConfig specifies S3-compatible protocol object storage destination.
type S3BackupConfig struct {
	Enabled        bool   `toml:"enabled"`
	Endpoint       string `toml:"endpoint"`
	Bucket         string `toml:"bucket"`
	Region         string `toml:"region,omitempty"`
	Prefix         string `toml:"prefix,omitempty"`
	AccessKeyEnv   string `toml:"access_key_env,omitempty"`
	SecretKeyEnv   string `toml:"secret_key_env,omitempty"`
	ForcePathStyle bool   `toml:"force_path_style,omitempty"`
	UseSSL         bool   `toml:"use_ssl,omitempty"`
	StorageClass   string `toml:"storage_class,omitempty"`
}

// PostgresReplicationConfig specifies secondary PostgreSQL instance replication.
type PostgresReplicationConfig struct {
	Enabled        bool   `toml:"enabled"`
	TargetDBURL    string `toml:"target_db_url"`
	Mode           string `toml:"mode,omitempty"` // "snapshot" or "continuous"
	TimeoutSeconds int    `toml:"timeout_seconds,omitempty"`
}

// BackupScheduleConfig specifies automated cadence and retention pruning.
type BackupScheduleConfig struct {
	AutoBackup       bool `toml:"auto_backup"`
	IntervalHours    int  `toml:"interval_hours"`
	RetentionCount   int  `toml:"retention_count"`
	SnapshotOnSubmit bool `toml:"snapshot_on_submit,omitempty"`
}

// CritiqueConfig holds review web service options.
type CritiqueConfig struct {
	Host                              string              `toml:"host"`
	Port                              int                 `toml:"port"`
	ProxyPort                         int                 `toml:"proxy_port"`
	DefaultReviewer                   string              `toml:"default_reviewer"`
	RequiredApprovals                 *int                `toml:"required_approvals,omitempty"`
	AllowSubmitWithUnresolvedComments *bool               `toml:"allow_submit_with_unresolved_comments,omitempty"`
	RequireGreenCheckRuns             *bool               `toml:"require_green_check_runs,omitempty"`
	AgentAuthorPatterns               []string            `toml:"agent_author_patterns,omitempty"`
	Auth                              CritiqueAuthConfig  `toml:"auth"`
	Groups                            CritiqueGroupConfig `toml:"groups"`
	Theme                             ThemeConfig         `toml:"theme"`
	AI                                CritiqueAIConfig    `toml:"ai"`
}

// CritiqueAIConfig holds settings for AI-assisted review and inline comment hints.
type CritiqueAIConfig struct {
	Enabled          bool            `toml:"enabled"`
	Endpoint         string          `toml:"endpoint"`
	APIKey           string          `toml:"api_key,omitempty"`
	APIKeyEnv        string          `toml:"api_key_env,omitempty"`
	Model            string          `toml:"model"`
	TimeoutSeconds   int             `toml:"timeout_seconds"`
	Temperature      float64         `toml:"temperature"`
	SystemPromptPath string          `toml:"system_prompt_path"`
	MaxContextLines  int             `toml:"max_context_lines"`
	AgentName        string          `toml:"agent_name"`
	TLS              ClientTLSConfig `toml:"tls"`
}

// IsConfigured returns true only when AI hints are enabled and an OpenAI-compatible endpoint URL is configured.
func (a *CritiqueAIConfig) IsConfigured() bool {
	if a == nil {
		return false
	}
	return a.Enabled && strings.TrimSpace(a.Endpoint) != ""
}

// ResolveAPIKey returns the API key from APIKey or APIKeyEnv.
func (a *CritiqueAIConfig) ResolveAPIKey() string {
	if a.APIKey != "" {
		return a.APIKey
	}
	if a.APIKeyEnv != "" {
		if val := os.Getenv(a.APIKeyEnv); val != "" {
			return val
		}
	}
	return ""
}

// DefaultAgentAuthorPatterns defines the default author patterns classified as automated agents.
var DefaultAgentAuthorPatterns = []string{
	"agent/*",
	"bot/*",
	"*[bot]",
	"*@agent*",
	"ci/*",
	"system",
}

// GetAgentAuthorPatterns returns the configured agent author patterns or defaults.
func (c *CritiqueConfig) GetAgentAuthorPatterns() []string {
	if len(c.AgentAuthorPatterns) > 0 {
		return c.AgentAuthorPatterns
	}
	return DefaultAgentAuthorPatterns
}

// IsAgentAuthor returns whether the given author name matches agent author patterns.
func (c *CritiqueConfig) IsAgentAuthor(author string) bool {
	return MatchAgentAuthor(author, c.GetAgentAuthorPatterns())
}

// MatchAgentAuthor tests author against a list of agent patterns (supporting prefixes like 'agent/*',
// suffixes like '*[bot]', wildcards like '*@agent*', or exact names).
func MatchAgentAuthor(author string, patterns []string) bool {
	authorLower := strings.ToLower(strings.TrimSpace(author))
	if authorLower == "" {
		return false
	}
	for _, pattern := range patterns {
		patternLower := strings.ToLower(strings.TrimSpace(pattern))
		if patternLower == "" {
			continue
		}
		if patternLower == authorLower {
			return true
		}
		if strings.HasSuffix(patternLower, "/*") {
			prefix := strings.TrimSuffix(patternLower, "*") // "agent/"
			if strings.HasPrefix(authorLower, prefix) {
				return true
			}
		}
		switch {
		case strings.HasPrefix(patternLower, "*") && strings.HasSuffix(patternLower, "*"):
			substr := strings.Trim(patternLower, "*")
			if strings.Contains(authorLower, substr) {
				return true
			}
		case strings.HasPrefix(patternLower, "*"):
			suffix := strings.TrimPrefix(patternLower, "*")
			if strings.HasSuffix(authorLower, suffix) {
				return true
			}
		case strings.HasSuffix(patternLower, "*"):
			prefix := strings.TrimSuffix(patternLower, "*")
			if strings.HasPrefix(authorLower, prefix) {
				return true
			}
		}
		if matched, err := filepath.Match(patternLower, authorLower); err == nil && matched {
			return true
		}
	}
	return false
}

// GetRequiredApprovals returns the configured required approvals count,
// or intelligently defaults to 0 in local/tailscale mode and 1 in multi-user/remote modes.
func (c *CritiqueConfig) GetRequiredApprovals() int {
	if c.RequiredApprovals != nil {
		return *c.RequiredApprovals
	}
	mode := AuthMode(strings.ToLower(strings.TrimSpace(c.Auth.Mode)))
	if mode == AuthModeLocal || mode == AuthModeTailscale || mode == "" {
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

// GetRequireGreenCheckRuns returns whether CLs require green CI check runs before submission.
// Defaults to false unless explicitly configured.
func (c *CritiqueConfig) GetRequireGreenCheckRuns() bool {
	if c.RequireGreenCheckRuns != nil {
		return *c.RequireGreenCheckRuns
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
			DBURL:     "postgres:///niebla?host=/run/postgresql",
			BlobPath:  "/devpool/niebla_blobs",
			CacheSize: 10000,
		},
		FS: FSConfig{
			MountBase: "$HOME/niebla/workspaces",
			CacheDir:  "$HOME/.cache/niebla/blobs",
		},
		Critique: CritiqueConfig{
			Host:                "0.0.0.0",
			Port:                8080,
			ProxyPort:           7331,
			DefaultReviewer:     "$USER",
			AgentAuthorPatterns: DefaultAgentAuthorPatterns,
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
			AI: CritiqueAIConfig{
				Enabled:          false,
				Endpoint:         "",
				APIKeyEnv:        "",
				Model:            "",
				TimeoutSeconds:   30,
				Temperature:      0.2,
				SystemPromptPath: ".niebla/prompts/comment_hint.md",
				MaxContextLines:  1200,
				AgentName:        "ai-hint-agent",
			},
		},
		Events: EventsConfig{
			Enabled: false,
			Sink:    "noop",
			Webhook: WebhookSinkConfig{
				TimeoutMS:  3000,
				MaxRetries: 3,
			},
			Exec: ExecSinkConfig{
				TimeoutMS: 5000,
			},
		},
		Resilience: ResilienceConfig{
			Enabled:     false,
			DefaultMode: "zfs_stream",
			ZFS: ZFSReplicationConfig{
				RawEncryptedSend: true,
				RemoteSSHPort:    22,
				SnapshotPrefix:   "niebla-auto",
				RetentionCount:   30,
			},
			Bundle: BundleConfig{
				Compression:      "zstd",
				CompressionLevel: 1,
			},
			Encryption: EncryptionConfig{
				Algorithm: "aes-256-gcm",
				KeySource: "env",
				KeyEnv:    "NIEBLA_MASTER_KEY",
			},
			Filesystem: FilesystemBackupConfig{
				BufferSizeMB: 8,
			},
			Nextcloud: NextcloudConfig{
				TimeoutSeconds: 180,
			},
			SFTP: SFTPConfig{
				TimeoutSeconds: 60,
			},
			S3: S3BackupConfig{
				Region:       "us-east-1",
				StorageClass: "STANDARD",
				UseSSL:       true,
			},
			PostgresReplication: PostgresReplicationConfig{
				Mode:           "snapshot",
				TimeoutSeconds: 300,
			},
			Schedule: BackupScheduleConfig{
				IntervalHours:    4,
				RetentionCount:   30,
				SnapshotOnSubmit: true,
			},
		},
		CI: CIConfig{
			Enabled:              false,
			DefaultRunner:        "local",
			RequireGreenToSubmit: false,
			Runners: map[string]CIRunnerConfig{
				"local": {
					Provider:       "local",
					WorkDir:        "/tmp/niebla-ci",
					Command:        []string{"go", "test", "./..."},
					TimeoutSeconds: 300,
					Concurrency:    2,
				},
			},
		},
	}
}

// CIConfig specifies automated CI runner orchestration settings.
type CIConfig struct {
	Enabled              bool                      `toml:"enabled"`
	DefaultRunner        string                    `toml:"default_runner"`          // e.g. "local"
	RequireGreenToSubmit bool                      `toml:"require_green_to_submit"` // Gates SubmitCL on green check runs
	ServerURL            string                    `toml:"server_url,omitempty"`    // Base URL for runner callbacks (e.g. http://localhost:8080)
	Runners              map[string]CIRunnerConfig `toml:"runners"`
}

// CIRunnerConfig defines a single declarative CI runner target.
type CIRunnerConfig struct {
	Provider       string       `toml:"provider"` // "local", "forgejo", "github", "webhook"
	WorkDir        string       `toml:"work_dir,omitempty"`
	Command        []string     `toml:"command,omitempty"`
	TimeoutSeconds int          `toml:"timeout_seconds,omitempty"`
	Concurrency    int          `toml:"concurrency,omitempty"`
	Endpoint       string       `toml:"endpoint,omitempty"`
	Ref            string       `toml:"ref,omitempty"`
	EventType      string       `toml:"event_type,omitempty"`
	Auth           CIAuthConfig `toml:"auth,omitempty"`
}

// CIAuthConfig specifies credentials and signing for remote runner dispatch.
type CIAuthConfig struct {
	Mode        string `toml:"mode"` // "bearer", "token", "basic", "header", "hmac", or "none"
	Token       string `toml:"token,omitempty"`
	TokenEnv    string `toml:"token_env,omitempty"`
	KeyFile     string `toml:"key_file,omitempty"`
	Username    string `toml:"username,omitempty"`
	Password    string `toml:"password,omitempty"`
	PasswordEnv string `toml:"password_env,omitempty"`
	HeaderName  string `toml:"header_name,omitempty"`
	Secret      string `toml:"secret,omitempty"`
	SecretEnv   string `toml:"secret_env,omitempty"`
}

// ResolveToken returns the API/bearer token from Token, TokenEnv, or KeyFile.
func (a *CIAuthConfig) ResolveToken() string {
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

// ResolveSecret returns the webhook HMAC secret from Secret or SecretEnv.
func (a *CIAuthConfig) ResolveSecret() []byte {
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
func (a *CIAuthConfig) ResolvePassword() string {
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
