package config

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/alecthomas/chroma/v2/styles"
)

// SupportedPresets lists all officially built-in theme presets.
var SupportedPresets = []string{
	"catppuccin-mocha",
	"catppuccin-latte",
	"catppuccin-macchiato",
	"catppuccin-frappe",
	"tokyo-night",
	"dracula",
	"nord",
	"github-dark",
	"custom",
}

var (
	hexColorRegex = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)
	cssColorRegex = regexp.MustCompile(`^(rgba?|hsla?)\(\s*[\d\.\%]+[\s,]+[\d\.\%]+[\s,]+[\d\.\%]+([\s,\/]+[\d\.\%]+)?\s*\)$`)
)

// ValidationError represents a specific configuration problem with context and actionable hint.
type ValidationError struct {
	Field   string
	Value   string
	Message string
	Hint    string
}

func (e ValidationError) Error() string {
	if e.Hint != "" {
		return fmt.Sprintf("• [%s]: %s\n    Hint: %s", e.Field, e.Message, e.Hint)
	}
	return fmt.Sprintf("• [%s]: %s", e.Field, e.Message)
}

// ValidationErrors is a slice of ValidationError that implements error.
type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("configuration validation failed (%d error(s) found):\n", len(ve)))
	for _, err := range ve {
		sb.WriteString("  " + err.Error() + "\n")
	}
	return sb.String()
}

// Validate checks all fields in Config and returns a descriptive ValidationErrors if invalid.
func Validate(cfg *Config) error {
	if cfg == nil {
		return ValidationErrors{
			{
				Field:   "root",
				Message: "configuration object is nil",
				Hint:    "Ensure config.toml is non-empty and contains valid TOML syntax.",
			},
		}
	}

	var errs ValidationErrors

	// 1. Version validation
	if cfg.Version < 1 {
		errs = append(errs, ValidationError{
			Field:   "version",
			Value:   fmt.Sprintf("%d", cfg.Version),
			Message: "version must be at least 1",
			Hint:    "Set 'version = 1' at the top level of your configuration file.",
		})
	}

	// 2. Core settings validation
	if cfg.Core.ServerAddr != "" {
		host, port, err := net.SplitHostPort(cfg.Core.ServerAddr)
		if err != nil || host == "" || port == "" {
			errs = append(errs, ValidationError{
				Field:   "core.server_addr",
				Value:   cfg.Core.ServerAddr,
				Message: "invalid server address format",
				Hint:    "Expected 'host:port' format (e.g. '127.0.0.1:50051' or 'localhost:50051').",
			})
		}
	}
	if cfg.Core.DefaultWorkspace != "" && strings.ContainsAny(cfg.Core.DefaultWorkspace, "/\\ \t\r\n") {
		errs = append(errs, ValidationError{
			Field:   "core.default_workspace",
			Value:   cfg.Core.DefaultWorkspace,
			Message: "default_workspace contains invalid characters",
			Hint:    "Workspace names cannot contain path separators or whitespace (e.g. 'default' or 'main').",
		})
	}

	// 3. Server daemon validation
	if cfg.Server.Port != 0 && (cfg.Server.Port < 1 || cfg.Server.Port > 65535) {
		errs = append(errs, ValidationError{
			Field:   "server.port",
			Value:   fmt.Sprintf("%d", cfg.Server.Port),
			Message: fmt.Sprintf("port %d is out of valid range", cfg.Server.Port),
			Hint:    "TCP port must be between 1 and 65535 (default: 50051).",
		})
	}
	if cfg.Server.CacheSize < 0 {
		errs = append(errs, ValidationError{
			Field:   "server.cache_size",
			Value:   fmt.Sprintf("%d", cfg.Server.CacheSize),
			Message: "cache_size cannot be negative",
			Hint:    "Specify 0 to disable memory caching, or a positive integer (default: 10000).",
		})
	}
	if cfg.Server.DBURL != "" {
		if !strings.HasPrefix(cfg.Server.DBURL, "postgres://") &&
			!strings.HasPrefix(cfg.Server.DBURL, "postgresql://") &&
			!strings.Contains(cfg.Server.DBURL, "=") {
			errs = append(errs, ValidationError{
				Field:   "server.db_url",
				Value:   cfg.Server.DBURL,
				Message: "unsupported PostgreSQL connection string format",
				Hint:    "Use a URI format like 'postgres:///niebla?host=/run/postgresql' or key-value format.",
			})
		}
	}

	// 4. FS configuration validation
	if cfg.FS.MountBase != "" && !strings.HasPrefix(cfg.FS.MountBase, "/") {
		errs = append(errs, ValidationError{
			Field:   "fs.mount_base",
			Value:   cfg.FS.MountBase,
			Message: "mount_base must be an absolute path",
			Hint:    "Specify an absolute directory path starting with '/' (e.g. '/niebla').",
		})
	}

	// 5. Critique HTTP server validation
	if cfg.Critique.Port != 0 && (cfg.Critique.Port < 1 || cfg.Critique.Port > 65535) {
		errs = append(errs, ValidationError{
			Field:   "critique.port",
			Value:   fmt.Sprintf("%d", cfg.Critique.Port),
			Message: fmt.Sprintf("port %d is out of valid range", cfg.Critique.Port),
			Hint:    "HTTP port must be between 1 and 65535 (default: 8080).",
		})
	}
	if cfg.Critique.ProxyPort != 0 && (cfg.Critique.ProxyPort < 1 || cfg.Critique.ProxyPort > 65535) {
		errs = append(errs, ValidationError{
			Field:   "critique.proxy_port",
			Value:   fmt.Sprintf("%d", cfg.Critique.ProxyPort),
			Message: fmt.Sprintf("proxy_port %d is out of valid range", cfg.Critique.ProxyPort),
			Hint:    "Live reload proxy port must be between 1 and 65535 (default: 7331).",
		})
	}

	// Inter-service port collision checks
	if cfg.Critique.Port != 0 && cfg.Critique.Port == cfg.Critique.ProxyPort {
		errs = append(errs, ValidationError{
			Field:   "critique.proxy_port",
			Value:   fmt.Sprintf("%d", cfg.Critique.ProxyPort),
			Message: "critique.proxy_port conflicts with critique.port",
			Hint:    "The live reload proxy and upstream HTTP server must listen on different ports (e.g. 7331 and 8080).",
		})
	}
	if cfg.Server.Port != 0 && cfg.Server.Port == cfg.Critique.Port {
		errs = append(errs, ValidationError{
			Field:   "critique.port",
			Value:   fmt.Sprintf("%d", cfg.Critique.Port),
			Message: "critique.port conflicts with server.port",
			Hint:    "Niebla gRPC server and Cotejo web UI must use different ports (e.g. 50051 and 8080).",
		})
	}
	if cfg.Server.Port != 0 && cfg.Server.Port == cfg.Critique.ProxyPort {
		errs = append(errs, ValidationError{
			Field:   "critique.proxy_port",
			Value:   fmt.Sprintf("%d", cfg.Critique.ProxyPort),
			Message: "critique.proxy_port conflicts with server.port",
			Hint:    "Niebla gRPC server and Cotejo live-reload proxy must use different ports (e.g. 50051 and 7331).",
		})
	}

	// Critique default reviewer validation
	if cfg.Critique.DefaultReviewer != "" && strings.ContainsAny(cfg.Critique.DefaultReviewer, " \t\r\n") {
		errs = append(errs, ValidationError{
			Field:   "critique.default_reviewer",
			Value:   cfg.Critique.DefaultReviewer,
			Message: "default_reviewer cannot contain whitespace",
			Hint:    "Specify a valid username like 'alice' or '$USER'.",
		})
	}

	// Critique auth mode validation
	if cfg.Critique.Auth.Mode != "" {
		authMode := strings.ToLower(strings.TrimSpace(cfg.Critique.Auth.Mode))
		if authMode != "local" && authMode != "headers" && authMode != "tailscale" {
			errs = append(errs, ValidationError{
				Field:   "critique.auth.mode",
				Value:   cfg.Critique.Auth.Mode,
				Message: fmt.Sprintf("unknown auth mode %q", cfg.Critique.Auth.Mode),
				Hint:    "Supported auth modes: 'local' (default: $USER), 'headers' (reverse proxy/IAP), 'tailscale'.",
			})
		}
	}

	// Required approvals validation
	if cfg.Critique.RequiredApprovals != nil && *cfg.Critique.RequiredApprovals < 0 {
		errs = append(errs, ValidationError{
			Field:   "critique.required_approvals",
			Value:   fmt.Sprintf("%d", *cfg.Critique.RequiredApprovals),
			Message: "required_approvals cannot be negative",
			Hint:    "Set 'required_approvals = 0' for solo workflows or a positive integer (e.g. 1 or 2).",
		})
	}

	// Groups mode validation
	if cfg.Critique.Groups.Mode != "" {
		gMode := strings.ToLower(strings.TrimSpace(cfg.Critique.Groups.Mode))
		if gMode != "static" && gMode != "api" && gMode != "disabled" {
			errs = append(errs, ValidationError{
				Field:   "critique.groups.mode",
				Value:   cfg.Critique.Groups.Mode,
				Message: fmt.Sprintf("unknown groups mode %q", cfg.Critique.Groups.Mode),
				Hint:    "Supported groups modes: 'static' (config-defined), 'api' (remote directory endpoint), or 'disabled'.",
			})
		}
		if gMode == "api" && strings.TrimSpace(cfg.Critique.Groups.API.URL) == "" {
			errs = append(errs, ValidationError{
				Field:   "critique.groups.api.url",
				Value:   "",
				Message: "groups API URL cannot be empty when mode is 'api'",
				Hint:    "Provide a valid directory lookup URL (e.g. 'https://identity.corp.internal/api/groups').",
			})
		}
	}

	// 3. Critique Theme validation
	theme := cfg.Critique.Theme
	if theme.Preset != "" {
		normalizedPreset := strings.ToLower(strings.TrimSpace(theme.Preset))
		if !isPresetSupported(normalizedPreset) {
			closest := findClosestPreset(normalizedPreset)
			hint := fmt.Sprintf("Supported presets: %s.", strings.Join(SupportedPresets, ", "))
			if closest != "" {
				hint = fmt.Sprintf("Did you mean %q? %s", closest, hint)
			}
			errs = append(errs, ValidationError{
				Field:   "critique.theme.preset",
				Value:   theme.Preset,
				Message: fmt.Sprintf("unknown theme preset %q", theme.Preset),
				Hint:    hint,
			})
		}
	}

	// 4. Chroma Syntax Style validation
	if theme.SyntaxStyle != "" {
		styleName := strings.TrimSpace(theme.SyntaxStyle)
		if styles.Registry[styleName] == nil {
			errs = append(errs, ValidationError{
				Field:   "critique.theme.syntax_style",
				Value:   theme.SyntaxStyle,
				Message: fmt.Sprintf("unrecognized Chroma syntax style %q", theme.SyntaxStyle),
				Hint:    "Choose a valid Chroma style such as 'catppuccin-mocha', 'tokyonight-night', 'dracula', 'nord', or 'github-dark'.",
			})
		}
	}

	// 5. Custom Color Hex validation
	colorFields := map[string]string{
		"critique.theme.colors.bg_canvas":        theme.Colors.BGCanvas,
		"critique.theme.colors.bg_surface":       theme.Colors.BGSurface,
		"critique.theme.colors.bg_card":          theme.Colors.BGCard,
		"critique.theme.colors.border":           theme.Colors.Border,
		"critique.theme.colors.text_primary":     theme.Colors.TextPrimary,
		"critique.theme.colors.text_muted":       theme.Colors.TextMuted,
		"critique.theme.colors.accent":           theme.Colors.Accent,
		"critique.theme.colors.diff_add_bg":      theme.Colors.DiffAddBG,
		"critique.theme.colors.diff_add_border":  theme.Colors.DiffAddBorder,
		"critique.theme.colors.diff_del_bg":      theme.Colors.DiffDelBG,
		"critique.theme.colors.diff_del_border":  theme.Colors.DiffDelBorder,
		"critique.theme.colors.diff_mod_left_bg": theme.Colors.DiffModLeftBG,
		"critique.theme.colors.diff_mod_right_bg": theme.Colors.DiffModRightBG,
	}

	for field, val := range colorFields {
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		if !isValidColor(val) {
			errs = append(errs, ValidationError{
				Field:   field,
				Value:   val,
				Message: fmt.Sprintf("invalid color format %q", val),
				Hint:    "Must be a hex color code (e.g. '#1e1e2e') or CSS color string (e.g. 'rgba(166, 227, 161, 0.20)').",
			})
		}
	}

	// 8. Events and notification subsystem validation
	if cfg.Events.Enabled {
		sink := strings.ToLower(strings.TrimSpace(cfg.Events.Sink))
		validSinks := map[string]bool{
			"webhook": true,
			"exec":    true,
			"log":     true,
			"noop":    true,
		}
		if sink == "" {
			errs = append(errs, ValidationError{
				Field:   "events.sink",
				Value:   cfg.Events.Sink,
				Message: "sink must be specified when events are enabled",
				Hint:    "Set 'sink = \"webhook\"', 'sink = \"exec\"', or 'sink = \"log\"'.",
			})
		} else if !validSinks[sink] {
			errs = append(errs, ValidationError{
				Field:   "events.sink",
				Value:   cfg.Events.Sink,
				Message: fmt.Sprintf("unsupported event sink driver %q", cfg.Events.Sink),
				Hint:    "Supported sink drivers: 'webhook', 'exec', 'log', 'noop'.",
			})
		}

		if sink == "webhook" {
			ep := strings.TrimSpace(cfg.Events.Webhook.Endpoint)
			if ep == "" {
				errs = append(errs, ValidationError{
					Field:   "events.webhook.endpoint",
					Value:   ep,
					Message: "webhook endpoint cannot be empty when webhook sink is active",
					Hint:    "Provide a valid HTTP or HTTPS destination URL (e.g. 'https://events.internal.corp/webhook').",
				})
			} else if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
				errs = append(errs, ValidationError{
					Field:   "events.webhook.endpoint",
					Value:   ep,
					Message: "webhook endpoint must use http:// or https:// protocol",
					Hint:    "Ensure endpoint starts with 'http://' or 'https://'.",
				})
			}

			if cfg.Events.Webhook.TimeoutMS < 0 || cfg.Events.Webhook.TimeoutMS > 30000 {
				errs = append(errs, ValidationError{
					Field:   "events.webhook.timeout_ms",
					Value:   fmt.Sprintf("%d", cfg.Events.Webhook.TimeoutMS),
					Message: "timeout_ms must be between 0 and 30000 milliseconds",
					Hint:    "Set a reasonable timeout such as 3000ms (3 seconds).",
				})
			}

			if cfg.Events.Webhook.MaxRetries < 0 || cfg.Events.Webhook.MaxRetries > 10 {
				errs = append(errs, ValidationError{
					Field:   "events.webhook.max_retries",
					Value:   fmt.Sprintf("%d", cfg.Events.Webhook.MaxRetries),
					Message: "max_retries must be between 0 and 10",
					Hint:    "Set retries to 3 or 0 to disable retries.",
				})
			}
		}

		if sink == "exec" {
			cmd := strings.TrimSpace(cfg.Events.Exec.Command)
			if cmd == "" {
				errs = append(errs, ValidationError{
					Field:   "events.exec.command",
					Value:   cmd,
					Message: "command cannot be empty when exec sink is active",
					Hint:    "Provide the path to an executable script or binary (e.g. '/usr/local/bin/router.sh').",
				})
			}
		}
	}

	// 9. Resilience, replication, and backup subsystem validation
	if cfg.Resilience.Enabled {
		mode := strings.ToLower(strings.TrimSpace(cfg.Resilience.DefaultMode))
		if mode == "" {
			mode = "zfs_stream"
		}
		if mode != "zfs_stream" && mode != "bundle" {
			errs = append(errs, ValidationError{
				Field:   "resilience.default_mode",
				Value:   cfg.Resilience.DefaultMode,
				Message: fmt.Sprintf("unsupported default_mode %q", cfg.Resilience.DefaultMode),
				Hint:    "Supported modes: 'zfs_stream' (default for 10G ZFS pools) or 'bundle' (encrypted archive packages).",
			})
		}

		if mode == "zfs_stream" && !cfg.Resilience.ZFS.Enabled {
			errs = append(errs, ValidationError{
				Field:   "resilience.zfs.enabled",
				Value:   "false",
				Message: "zfs replication must be enabled when default_mode is 'zfs_stream'",
				Hint:    "Set 'resilience.zfs.enabled = true' or set 'default_mode = \"bundle\"'.",
			})
		}

		if cfg.Resilience.ZFS.Enabled {
			if strings.TrimSpace(cfg.Resilience.ZFS.LocalDataset) == "" {
				errs = append(errs, ValidationError{
					Field:   "resilience.zfs.local_dataset",
					Value:   cfg.Resilience.ZFS.LocalDataset,
					Message: "local_dataset cannot be empty when ZFS replication is active",
					Hint:    "Specify the local ZFS dataset (e.g. 'localpool/niebla').",
				})
			}
			if strings.TrimSpace(cfg.Resilience.ZFS.RemoteDataset) == "" {
				errs = append(errs, ValidationError{
					Field:   "resilience.zfs.remote_dataset",
					Value:   cfg.Resilience.ZFS.RemoteDataset,
					Message: "remote_dataset cannot be empty when ZFS replication is active",
					Hint:    "Specify destination dataset on remote server (e.g. 'tank/niebla-replica').",
				})
			}
			if strings.TrimSpace(cfg.Resilience.ZFS.RemoteHost) == "" {
				errs = append(errs, ValidationError{
					Field:   "resilience.zfs.remote_host",
					Value:   cfg.Resilience.ZFS.RemoteHost,
					Message: "remote_host cannot be empty when ZFS replication is active",
					Hint:    "Specify remote ZFS server hostname or IP address.",
				})
			}
		}

		if cfg.Resilience.Encryption.Algorithm != "" {
			algo := strings.ToLower(strings.TrimSpace(cfg.Resilience.Encryption.Algorithm))
			if algo != "aes-256-gcm" && algo != "chacha20-poly1305" {
				errs = append(errs, ValidationError{
					Field:   "resilience.encryption.algorithm",
					Value:   cfg.Resilience.Encryption.Algorithm,
					Message: fmt.Sprintf("unsupported encryption algorithm %q", cfg.Resilience.Encryption.Algorithm),
					Hint:    "Supported algorithms: 'aes-256-gcm' (recommended) or 'chacha20-poly1305'.",
				})
			}
		}

		if cfg.Resilience.Encryption.KeySource != "" {
			ks := strings.ToLower(strings.TrimSpace(cfg.Resilience.Encryption.KeySource))
			if ks != "env" && ks != "file" && ks != "passphrase" {
				errs = append(errs, ValidationError{
					Field:   "resilience.encryption.key_source",
					Value:   cfg.Resilience.Encryption.KeySource,
					Message: fmt.Sprintf("unknown key_source %q", cfg.Resilience.Encryption.KeySource),
					Hint:    "Supported key sources: 'env', 'file', or 'passphrase'.",
				})
			}
			if ks == "env" && strings.TrimSpace(cfg.Resilience.Encryption.KeyEnv) == "" {
				errs = append(errs, ValidationError{
					Field:   "resilience.encryption.key_env",
					Value:   "",
					Message: "key_env must be specified when key_source is 'env'",
					Hint:    "Set 'key_env = \"NIEBLA_MASTER_KEY\"' or another environment variable name.",
				})
			}
			if ks == "passphrase" && strings.TrimSpace(cfg.Resilience.Encryption.PassphraseEnv) == "" {
				errs = append(errs, ValidationError{
					Field:   "resilience.encryption.passphrase_env",
					Value:   "",
					Message: "passphrase_env must be specified when key_source is 'passphrase'",
					Hint:    "Set 'passphrase_env = \"NIEBLA_BACKUP_PASSPHRASE\"'.",
				})
			}
		}

		if cfg.Resilience.Nextcloud.Enabled {
			u := strings.TrimSpace(cfg.Resilience.Nextcloud.URL)
			if u == "" || (!strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://")) {
				errs = append(errs, ValidationError{
					Field:   "resilience.nextcloud.url",
					Value:   u,
					Message: "nextcloud url must begin with http:// or https://",
					Hint:    "Specify full WebDAV path (e.g. 'https://nextcloud.domain.com/remote.php/dav/files/user/niebla').",
				})
			}
			if strings.TrimSpace(cfg.Resilience.Nextcloud.Username) == "" {
				errs = append(errs, ValidationError{
					Field:   "resilience.nextcloud.username",
					Value:   "",
					Message: "username cannot be empty when Nextcloud backup is enabled",
					Hint:    "Specify your Nextcloud username.",
				})
			}
			if strings.TrimSpace(cfg.Resilience.Nextcloud.PasswordEnv) == "" {
				errs = append(errs, ValidationError{
					Field:   "resilience.nextcloud.password_env",
					Value:   "",
					Message: "password_env cannot be empty when Nextcloud backup is enabled",
					Hint:    "Specify environment variable containing your Nextcloud App Password.",
				})
			}
		}

		if cfg.Resilience.S3.Enabled {
			if strings.TrimSpace(cfg.Resilience.S3.Endpoint) == "" {
				errs = append(errs, ValidationError{
					Field:   "resilience.s3.endpoint",
					Value:   "",
					Message: "endpoint cannot be empty when S3 backup is enabled",
					Hint:    "Specify S3 endpoint (e.g. 's3.us-east-1.amazonaws.com' or 'http://127.0.0.1:9000').",
				})
			}
			if strings.TrimSpace(cfg.Resilience.S3.Bucket) == "" {
				errs = append(errs, ValidationError{
					Field:   "resilience.s3.bucket",
					Value:   "",
					Message: "bucket cannot be empty when S3 backup is enabled",
					Hint:    "Specify destination S3 bucket name.",
				})
			}
			if strings.TrimSpace(cfg.Resilience.S3.AccessKeyEnv) == "" {
				errs = append(errs, ValidationError{
					Field:   "resilience.s3.access_key_env",
					Value:   "",
					Message: "access_key_env cannot be empty when S3 backup is enabled",
					Hint:    "Specify environment variable name containing S3 access key (default: 'AWS_ACCESS_KEY_ID').",
				})
			}
			if strings.TrimSpace(cfg.Resilience.S3.SecretKeyEnv) == "" {
				errs = append(errs, ValidationError{
					Field:   "resilience.s3.secret_key_env",
					Value:   "",
					Message: "secret_key_env cannot be empty when S3 backup is enabled",
					Hint:    "Specify environment variable name containing S3 secret key (default: 'AWS_SECRET_ACCESS_KEY').",
				})
			}
		}

		if cfg.Resilience.PostgresReplication.Enabled {
			targetURL := strings.TrimSpace(cfg.Resilience.PostgresReplication.TargetDBURL)
			if targetURL == "" || (!strings.HasPrefix(targetURL, "postgres://") && !strings.HasPrefix(targetURL, "postgresql://")) {
				errs = append(errs, ValidationError{
					Field:   "resilience.postgres_replication.target_db_url",
					Value:   targetURL,
					Message: "target_db_url must begin with postgres:// or postgresql://",
					Hint:    "Specify connection string to standby PostgreSQL instance.",
				})
			} else if targetURL == strings.TrimSpace(cfg.Server.DBURL) {
				errs = append(errs, ValidationError{
					Field:   "resilience.postgres_replication.target_db_url",
					Value:   targetURL,
					Message: "target_db_url cannot be identical to server.db_url (self-replication loop)",
					Hint:    "Target instance must be a distinct standby/replica database.",
				})
			}
		}
	}

	// 10. CI runner subsystem validation
	if cfg.CI.Enabled {
		if cfg.CI.DefaultRunner != "" && len(cfg.CI.Runners) > 0 {
			if _, exists := cfg.CI.Runners[cfg.CI.DefaultRunner]; !exists {
				errs = append(errs, ValidationError{
					Field:   "ci.default_runner",
					Value:   cfg.CI.DefaultRunner,
					Message: fmt.Sprintf("default_runner %q not found in runners table", cfg.CI.DefaultRunner),
					Hint:    "Ensure [ci.runners.<name>] matches the default_runner value or declare the runner.",
				})
			}
		}

		for name, r := range cfg.CI.Runners {
			provider := strings.ToLower(strings.TrimSpace(r.Provider))
			if provider == "" {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("ci.runners.%s.provider", name),
					Value:   "",
					Message: "provider cannot be empty",
					Hint:    "Set provider to 'local', 'forgejo', 'github', or 'webhook'.",
				})
			} else if provider != "local" && provider != "forgejo" && provider != "github" && provider != "webhook" {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("ci.runners.%s.provider", name),
					Value:   r.Provider,
					Message: fmt.Sprintf("unsupported CI provider %q", r.Provider),
					Hint:    "Supported CI providers: 'local', 'forgejo', 'github', or 'webhook'.",
				})
			}

			if provider == "forgejo" || provider == "github" || provider == "webhook" {
				ep := strings.TrimSpace(r.Endpoint)
				if ep == "" || (!strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://")) {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("ci.runners.%s.endpoint", name),
						Value:   ep,
						Message: "endpoint must begin with http:// or https://",
						Hint:    "Specify the HTTP/REST webhook or dispatch endpoint URL.",
					})
				}
			}

			if r.Auth.Mode != "" {
				authMode := strings.ToLower(strings.TrimSpace(r.Auth.Mode))
				validAuthModes := map[string]bool{
					"none": true, "bearer": true, "token": true, "basic": true, "header": true, "hmac": true,
				}
				if !validAuthModes[authMode] {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("ci.runners.%s.auth.mode", name),
						Value:   r.Auth.Mode,
						Message: fmt.Sprintf("unsupported auth mode %q", r.Auth.Mode),
						Hint:    "Supported auth modes: 'none', 'bearer', 'token', 'basic', 'header', 'hmac'.",
					})
				}

				if (authMode == "bearer" || authMode == "token") && r.Auth.Token == "" && r.Auth.TokenEnv == "" && r.Auth.KeyFile == "" {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("ci.runners.%s.auth", name),
						Value:   "",
						Message: fmt.Sprintf("auth mode %q requires token, token_env, or key_file", authMode),
						Hint:    "Set 'token = \"...\"' or 'token_env = \"ENV_VAR\"' or 'key_file = \"...\"'.",
					})
				}
				if authMode == "basic" && r.Auth.Username == "" {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("ci.runners.%s.auth.username", name),
						Value:   "",
						Message: "basic auth requires username",
						Hint:    "Set username for basic authentication.",
					})
				}
				if authMode == "header" && strings.TrimSpace(r.Auth.HeaderName) == "" {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("ci.runners.%s.auth.header_name", name),
						Value:   "",
						Message: "header auth requires header_name",
						Hint:    "Set header_name (e.g. 'X-Api-Key').",
					})
				}
				if authMode == "hmac" && r.Auth.Secret == "" && r.Auth.SecretEnv == "" {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("ci.runners.%s.auth", name),
						Value:   "",
						Message: "hmac auth requires secret or secret_env",
						Hint:    "Set secret or secret_env to configure HMAC signing key.",
					})
				}
			}
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func isPresetSupported(name string) bool {
	for _, p := range SupportedPresets {
		if p == name {
			return true
		}
	}
	return false
}

func isValidColor(val string) bool {
	return hexColorRegex.MatchString(val) || cssColorRegex.MatchString(val)
}

func findClosestPreset(input string) string {
	input = strings.ToLower(input)
	for _, p := range SupportedPresets {
		if strings.HasPrefix(p, input) || strings.Contains(p, input) {
			return p
		}
	}

	var bestMatch string
	minDist := 6
	for _, p := range SupportedPresets {
		d := levenshtein(input, p)
		if parts := strings.Split(p, "-"); len(parts) > 1 {
			if dPrefix := levenshtein(input, parts[0]); dPrefix < d {
				d = dPrefix
			}
		}
		if d < minDist {
			minDist = d
			bestMatch = p
		}
	}
	return bestMatch
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	d := make([][]int, la+1)
	for i := range d {
		d[i] = make([]int, lb+1)
		d[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		d[0][j] = j
	}

	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[i][j] = min(
				d[i-1][j]+1,      // deletion
				d[i][j-1]+1,      // insertion
				d[i-1][j-1]+cost, // substitution
			)
		}
	}
	return d[la][lb]
}

func min(vals ...int) int {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}
