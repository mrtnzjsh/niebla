package config

import "strings"

// Config represents the unified toolchain configuration stored in ~/.config/niebla/config.toml.
type Config struct {
	Version  int            `toml:"version"`
	Core     CoreConfig     `toml:"core"`
	Server   ServerConfig   `toml:"server"`
	FS       FSConfig       `toml:"fs"`
	Critique   CritiqueConfig   `toml:"critique"`
	Events     EventsConfig     `toml:"events"`
	Resilience ResilienceConfig `toml:"resilience"`
}

// CoreConfig holds settings shared across toolchain components.
type CoreConfig struct {
	ServerAddr       string `toml:"server_addr"`
	DefaultWorkspace string `toml:"default_workspace"`
}

// ServerConfig holds daemon settings for niebla-server.
type ServerConfig struct {
	Host      string `toml:"host"`
	Port      int    `toml:"port"`
	DBURL     string `toml:"db_url"`
	BlobPath  string `toml:"blob_path"`
	CacheSize int    `toml:"cache_size"`
}

// FSConfig holds filesystem mount and caching options for nieblafs.
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
			MountBase: "/niebla",
			CacheDir:  "$HOME/.cache/niebla/blobs",
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
	}
}
