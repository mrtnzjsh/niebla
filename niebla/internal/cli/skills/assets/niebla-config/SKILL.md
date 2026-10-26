---
name: niebla-config
description: >-
  Workflows, schema specification, theming customization, and fast-fail validation rules for managing
  the Niebla unified toolchain configuration (~/.config/niebla/config.toml). Use when inspecting,
  updating, validating, or switching visual themes and syntax highlighting for Cotejo.
---

# Niebla Toolchain Configuration & Theming Guide

This skill guides AI agents and developers in inspecting, modifying, and validating the unified configuration system for the Niebla toolchain (`niebla`, `niebla-server`, `cotejo`, and `nieblafs`).

---

## 1. Core Principles for AI Agents

1. **Canonical Config Location:**
   Configuration is rooted at:
   - `$XDG_CONFIG_HOME/niebla/config.toml` (default: `~/.config/niebla/config.toml`).
   - Overridable via `NIEBLA_CONFIG` environment variable or `--config <path>` flag.
2. **User Precedence & Non-Destructive Merges:**
   - On first run, Niebla initializes `~/.config/niebla/config.toml` from embedded baseline defaults.
   - Any value defined by the user strictly takes precedence over defaults.
   - Future updates to the toolchain automatically merge new default options without overwriting user customizations.
3. **Dynamic Environment Variable Expansion:**
   - Any string value containing `$USER`, `${USER}`, `$HOME`, or `${HOME}` is automatically resolved at runtime.
   - If `critique.default_reviewer` is set to `"$USER"` or omitted, it dynamically resolves to the active OS username.
4. **Mandatory Fast-Fail Validation:**
   - After proposing or applying any edits to `config.toml`, ALWAYS run `niebla config validate` to ensure syntax and semantics are 100% valid.

---

## 2. CLI Command Quick Reference

| Command | Description |
| :--- | :--- |
| `niebla config path` | Displays the absolute filesystem path of the active `config.toml`. |
| `niebla config validate [file]` | Fast-fail validation of syntax, port collisions, and theme presets. |
| `niebla config show` | Outputs the raw active configuration file. |
| `niebla config show --resolved` | Outputs fully evaluated configuration after 3-way merge and env expansion. |
| `niebla config init [--force]` | Initializes or resets config from canonical embedded defaults. |

---

## 3. Configuration Schema Reference

```toml
version = 1

[core]
# Address of the active Niebla gRPC repository service (host:port)
server_addr = "127.0.0.1:50051"
# Default workspace name if unspecified in CLI invocations
default_workspace = "default"

[server]
# Bind address for niebla-server daemon
host = "0.0.0.0"
port = 50051
# PostgreSQL connection URI (socket or TCP URL)
db_url = "postgres:///niebla?host=/run/postgresql"
# Local root path for CAS blob storage
blob_path = "/devpool/niebla_blobs"
# Maximum number of tree nodes cached in LRU memory
cache_size = 10000

[fs]
# Root mount directory for virtual FUSE workspaces (must be absolute)
mount_base = "/niebla"
# Cache directory for local blob chunks (supports $HOME expansion)
cache_dir = "$HOME/.cache/niebla/blobs"

[critique]
# Web review service bind address and port
host = "0.0.0.0"
port = 8080
# Live reload proxy port (must not collide with port or server.port)
proxy_port = 7331
# Default author identity for reviews (resolves to $USER if omitted)
default_reviewer = "$USER"

[critique.theme]
# Built-in presets: "catppuccin-mocha", "catppuccin-latte", "catppuccin-macchiato",
#                   "catppuccin-frappe", "tokyo-night", "dracula", "nord", "github-dark", "custom"
preset = "catppuccin-mocha"
# Chroma syntax highlighting style (e.g. "catppuccin-mocha", "tokyonight-night", "dracula")
syntax_style = "catppuccin-mocha"

[critique.theme.colors]
# Optional color overrides (hex e.g. "#1e1e2e" or css e.g. "rgba(166, 227, 161, 0.20)")
bg_canvas       = "#1e1e2e"
bg_surface      = "#181825"
bg_card         = "#11111b"
border          = "#313244"
text_primary    = "#cdd6f4"
text_muted      = "#a6adc8"
accent          = "#89b4fa"
diff_add_bg     = "rgba(166, 227, 161, 0.20)"
diff_add_border = "#a6e3a1"
diff_del_bg     = "rgba(243, 139, 168, 0.20)"
diff_del_border = "#f38ba8"

[resilience]
enabled = false
default_mode = "zfs_stream" # "zfs_stream" (default) or "bundle"

[resilience.zfs]
enabled = false
local_dataset = "zroot/data/niebla"
remote_host = "backup-node.internal"
remote_user = "niebla-repl"
remote_ssh_port = 22
remote_dataset = "backups/niebla"
raw_encrypted_send = true
snapshot_prefix = "niebla-auto"
retention_count = 7
bandwidth_limit_mb = 0

[resilience.bundle]
enabled = false
target_dir = "/mnt/zfs-pool/niebla-backups"
compression = "zstd"
compression_level = 3

[resilience.encryption]
algorithm = "aes-256-gcm" # "aes-256-gcm" or "chacha20-poly1305"
key_source = "env"        # "env", "file", "passphrase"
key_env = "NIEBLA_MASTER_KEY"
passphrase_env = "NIEBLA_BACKUP_PASSPHRASE"

[resilience.filesystem]
enabled = false
path = "/mnt/zfs-pool/niebla-backups"
buffer_size_mb = 8

[resilience.nextcloud]
enabled = false
url = "https://nextcloud.internal/remote.php/dav/files/niebla"
username = "niebla-backup"
password_env = "NIEBLA_NEXTCLOUD_PASSWORD"
timeout_seconds = 180

[resilience.s3]
enabled = false
endpoint = "s3.amazonaws.com"
bucket = "niebla-backups-bucket"
region = "us-east-1"
prefix = "niebla"
access_key_env = "AWS_ACCESS_KEY_ID"
secret_key_env = "AWS_SECRET_ACCESS_KEY"
force_path_style = false
use_ssl = true

[resilience.schedule]
auto_backup = false
interval_minutes = 60
retention_count = 14
prune_cadence_hours = 24
```

---

## 4. Cotejo Theming Engine & Presets

Cotejo includes pre-compiled, harmonious palettes:

* **`catppuccin-mocha` (Default):** Deep dark pastel canvas (`#1e1e2e`), blue accent (`#89b4fa`).
* **`catppuccin-macchiato`:** Medium dark canvas (`#24273a`), blue accent (`#8aadf4`).
* **`catppuccin-frappe`:** Low contrast matte dark (`#303446`), lavender/blue accent (`#8caaee`).
* **`catppuccin-latte`:** Crisp warm light mode (`#eff1f5`), blue accent (`#1e66f5`).
* **`tokyo-night`:** Cyberpunk indigo canvas (`#1a1b26`), cyan/blue accent (`#7aa2f7`).
* **`dracula`:** Classic gothic purple canvas (`#282a36`), purple accent (`#bd93f9`).
* **`nord`:** Arctic blue/gray canvas (`#2e3440`), frost accent (`#88c0d0`).
* **`github-dark`:** Monokai/GitHub canvas (`#0d1117`), blue accent (`#2f81f7`).
* **`custom`:** Pure custom palette; requires supplying hex color overrides under `[critique.theme.colors]`.

### Modifying Theme via AI Agents
When a user asks: *"Switch my Cotejo theme to Tokyo Night"*:
1. Locate the config: `~/.config/niebla/config.toml`.
2. Update `preset = "tokyo-night"` and `syntax_style = "tokyonight-night"`.
3. Validate: `niebla config validate`.
4. Notify the user: the live-reloading dev server hot-reloads the theme automatically.

---

## 5. Validation Rules & Fast-Fail Guarantees

When modifying `config.toml`, Niebla enforces:
1. `version`: Must be >= 1.
2. `server.port`, `critique.port`, `critique.proxy_port`: Range `[1, 65535]`.
3. **Port Collisions:** `critique.port != critique.proxy_port` and neither may collide with `server.port`.
4. `core.server_addr`: Must match `host:port` format.
5. `server.db_url`: Must begin with `postgres://`, `postgresql://`, or key=value syntax.
6. `fs.mount_base`: Must be an absolute path starting with `/`.
7. `critique.theme.preset`: Must match a known preset. Typos trigger Levenshtein distance suggestions (e.g. typing `cattppuccin` suggests `catppuccin-mocha`).
8. `critique.theme.syntax_style`: Must match a registered Chroma style.
9. `critique.theme.colors.*`: Must be valid `#RGB`, `#RRGGBB`, `#RRGGBBAA`, or `rgba(...)` color syntax.
10. **Resilience & Replication Rules:**
    - `resilience.default_mode`: Must be `"zfs_stream"` or `"bundle"`.
    - `resilience.zfs.local_dataset` & `remote_dataset`: Cannot contain whitespace or invalid characters.
    - `resilience.encryption.algorithm`: Must be `"aes-256-gcm"` or `"chacha20-poly1305"`.
    - `resilience.encryption.key_source`: Must be `"env"`, `"file"`, or `"passphrase"`.
    - `resilience.nextcloud.url`: Must be an `http://` or `https://` WebDAV URL.
    - `resilience.s3.bucket`: Must not be empty when S3 driver is enabled.
    - `resilience.postgres_replication.target_db_url`: Cannot be identical to primary `server.db_url`.

---

## 6. Leveraging the Validation Suite (Agent Workflows)

AI harnesses should always leverage Niebla's validation suite to ensure changes never break the running environment or introduce invalid states.

### A. Pre-Flight Validation Pattern (Safe Config Edits)
When modifying `~/.config/niebla/config.toml`, never write untested changes in-place:
1. **Prepare Candidate:** Write proposed edits to a temporary candidate file (e.g. `/tmp/candidate_config.toml`).
2. **Execute Validation Suite:**
   ```bash
   niebla config validate /tmp/candidate_config.toml
   ```
3. **Automated Self-Correction via Hints:**
   If `niebla config validate` exits with code 1, parse the output:
   ```
   ❌ Configuration validation failed:
     • [critique.theme.preset]: unknown theme preset "tokyonight"
       Hint: Did you mean "tokyo-night"? Supported presets: catppuccin-mocha, ...
   ```
   Inspect the `Hint:` line, apply the correction to the candidate, and re-run validation.
4. **Atomic Commit:**
   Once validation returns `✓ Configuration is valid`, copy the candidate file:
   ```bash
   cp /tmp/candidate_config.toml ~/.config/niebla/config.toml
   rm -f /tmp/candidate_config.toml
   ```
5. **Final Check:**
   ```bash
   niebla config validate
   ```

### B. Unit & Regression Test Suite (Code Changes)
When modifying the configuration schema, parsers, or adding new validation rules in Go:
1. Run the `internal/config` unit test suite:
   ```bash
   nix develop --command bash -c "cd niebla && go test -v ./internal/config/..."
   ```
   This validates:
   - `TestValidateDefaultConfig`: Baseline config integrity.
   - `TestValidatePortRanges` & `TestValidatePortCollision`: Network boundary rules.
   - `TestValidateThemePresetWithTypoSuggestion`: Levenshtein matching and suggestions.
   - `TestValidateThemeColors`: Hex and CSS color format regexes.
   - `TestValidateSyntaxStyle`: Chroma registry style lookup.
   - `TestLoadBytesValidOverride`: Smart 3-way merge precedence.
   - `TestLoadBytesInvalidTOMLSyntaxFailsFast`: Line/column syntax diagnostics.
   - `TestEnvironmentVariableExpansion`: `$USER` and `$HOME` dynamic resolution.

2. Run the CLI validation command tests:
   ```bash
   nix develop --command bash -c "cd niebla && go test -v -run TestConfig ./internal/cli/..."
   ```
