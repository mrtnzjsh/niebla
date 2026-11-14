---
title: "Unified Configuration & Multi-File Architecture"
description: Exhaustive guide to Niebla's unified configuration system, multi-file discovery, precedence hierarchy, fast-fail validation, and full TOML schema specification.
sidebar:
  order: 2
  label: Configuration & Multi-File Architecture
---

Niebla uses a unified, server-authoritative configuration model that governs all four core subsystems: the developer CLI (`niebla`), the central gRPC repository daemon (`niebla-server`), the Cotejo code review web application (`cotejo`), and the virtualized filesystem engine (`nieblafs`).

Rather than scattering dotfiles and bespoke flags across multiple tools, Niebla centralizes operational parameters into a modular TOML configuration system that supports automatic file discovery, non-destructive defaults merging, dynamic environment variable expansion, and strict pre-flight validation.

---

## 1. Configuration Philosophy & Discovery

Niebla enforces three fundamental architectural rules for toolchain configuration:

1. **Single Source of Truth:** A single directory (`$XDG_CONFIG_HOME/niebla/` or `~/.config/niebla/`) configures both client and server behaviors.
2. **Deterministic Precedence:** Explicit CLI flags override environment variables, which override workspace metadata, which override user configuration files, which override compile-time defaults.
3. **Fail-Fast Safety:** Configuration syntax errors, port collisions, or duplicate section declarations halt execution immediately with human-readable error messages and Levenshtein typo suggestions before any sockets or database pools open.

```mermaid
flowchart TD
    subgraph Precedence["Configuration Precedence Hierarchy"]
        direction TB
        P1["1. Explicit CLI Flags<br/><code>--server</code>, <code>--workspace</code>, <code>--config-dir</code>"]
        P2["2. Environment Variables<br/><code>NIEBLA_CONFIG</code>, <code>NIEBLA_SERVER</code>, <code>XDG_CONFIG_HOME</code>"]
        P3["3. Workspace-Level Control Markers<br/><code>.niebla/workspace.toml</code>"]
        P4["4. Modular User Configuration<br/><code>~/.config/niebla/*.toml</code> (Merged)"]
        P5["5. Embedded Base Defaults<br/>Compiled baseline fallbacks"]
        P1 --> P2 --> P3 --> P4 --> P5
    end
```

### Search Paths & Discovery Cascade

When Niebla initializes, it locates its active configuration through the following sequence:

1. **`NIEBLA_CONFIG` Environment Variable:**
   - If set to an existing directory path (e.g., `NIEBLA_CONFIG=/etc/niebla`), Niebla treats that directory as the configuration root and discovers all modular `*.toml` files within it.
   - If set to a specific file (e.g., `NIEBLA_CONFIG=/tmp/test-config.toml`), Niebla loads that single file directly.
2. **XDG Standard Directory:**
   - If `$XDG_CONFIG_HOME` is set, Niebla searches `$XDG_CONFIG_HOME/niebla/`.
3. **Standard User Home Directory:**
   - Falls back to `~/.config/niebla/` (or `%USERPROFILE%\.config\niebla\` on Windows).
4. **Automatic Base Initialization:**
   - If `~/.config/niebla/` does not exist or contains no `.toml` files, Niebla automatically creates the directory and scaffolds `~/.config/niebla/config.toml` from its embedded baseline template (`base_config.toml`) with permissions `0644`.

---

## 2. Multi-File Modularity (`~/.config/niebla/*.toml`)

Enterprise monorepos frequently involve distinct operational domains: server administrators manage database connections and TLS certs, UI designers tweak review themes, and AI teams maintain LLM endpoints. 

To prevent bloated, monolithic files and merge conflicts, Niebla natively supports **multi-file modular configuration**.

### File Discovery Sequence

When resolving a configuration directory, Niebla reads all non-hidden files ending in `.toml` using the following deterministic ordering:

1. **`config.toml` (Primary):** Loaded first if present.
2. **Alphabetical Sibling Files:** All other `.toml` files (e.g., `ai.toml`, `ci.toml`, `resilience.toml`, `theme.toml`) are sorted alphabetically and evaluated in sequence.
3. **Hidden Files Excluded:** Any file starting with a dot (e.g., `.backup.toml`, `.draft.toml`) is strictly ignored.

```
~/.config/niebla/
├── config.toml         # Core client, server daemon, and filesystem mounts
├── ai.toml             # LLM endpoint, API keys, and context apertures
├── ci.toml             # CI runners, Forgejo/GitHub dispatchers, gating rules
├── events.toml         # CloudEvents sinks and webhook routing
├── resilience.toml     # ZFS streaming, S3 backup buckets, encryption keys
└── theme.toml          # Cotejo colors, syntax styles, and UI presets
```

### The Strict Header & Subsection Uniqueness Rule

To guarantee absolute determinism and prevent silent override bugs, Niebla enforces a **strict section uniqueness rule**:

> **Rule:** No TOML table header or subsection (e.g., `[server]`, `[critique]`, `[critique.ai]`, `[resilience.s3]`) may be declared in more than one `.toml` file within the configuration directory.

#### Valid Modular Example

Defining different tables across files is fully supported:

```toml
# File: ~/.config/niebla/config.toml
version = 1

[core]
server_addr = "127.0.0.1:50051"
default_workspace = "default"

[critique]
port = 8080
default_reviewer = "$USER"
```

```toml
# File: ~/.config/niebla/ai.toml
[critique.ai]
enabled = true
endpoint = "https://api.openai.com/v1"
api_key_env = "OPENAI_API_KEY"
model = "gpt-4o"
```

```toml
# File: ~/.config/niebla/theme.toml
[critique.theme]
preset = "catppuccin-mocha"
syntax_style = "catppuccin-mocha"
```

#### Invalid Duplicate Example

Declaring the same table or sub-table in multiple files triggers a fast-fail error during discovery:

```toml
# File: ~/.config/niebla/config.toml
[critique.ai]
enabled = false
```

```toml
# File: ~/.config/niebla/ai.toml
[critique.ai]
enabled = true
endpoint = "https://api.openai.com/v1"
```

**Validator Output:**

```console
$ niebla config validate
❌ Configuration validation failed:
  • [critique.ai]: duplicate configuration section [critique.ai] defined in multiple files (config.toml:12 and ai.toml:1)
    Hint: Each header/subsection may only be defined in a single TOML file inside ~/.config/niebla/. Consolidate [critique.ai] into either config.toml or ai.toml.
```

---

## 3. Dynamic Environment Variable Expansion

Hardcoding user-specific paths or corporate usernames creates fragile configuration files that cannot be committed to dotfile repositories or shared across workstations.

Niebla dynamically expands environment variables in all string fields across any configuration file during unmarshaling:

- `$USER` or `${USER}`: Resolves to the active operating system user (e.g., `developer`).
- `$HOME` or `${HOME}`: Resolves to the user's home directory (e.g., `/home/developer` or `/Users/developer`).
- Any custom environment variable defined in your shell session.

### Automatic Reviewer Defaulting

In Cotejo, if `critique.default_reviewer` is set to `"$USER"` or omitted, Niebla dynamically resolves it to your active OS login name. When submitting comments or shelving CLs, your local username is automatically attributed as the author.

```toml
[critique]
# Resolves to the active OS login name dynamically
default_reviewer = "$USER"

[fs]
# Resolves to /home/developer/niebla/workspaces
mount_base = "$HOME/niebla/workspaces"
cache_dir = "$HOME/.cache/niebla/blobs"
```

---

## 4. Complete Configuration Schema

Below is the exhaustive reference of all configuration tables and fields available in Niebla.

### `[core]` (Global Client & gRPC Settings)

Shared settings utilized by the `niebla` CLI and client utilities.

```toml
[core]
# Address of the active Niebla gRPC repository service (host:port)
server_addr = "127.0.0.1:50051"

# Default workspace name if unspecified in CLI invocations
default_workspace = "default"

# Optional bearer token for gRPC authentication if niebla-server enforces auth
auth_token = ""

[core.tls]
# Enable TLS for client gRPC connections
enabled = false
# Path to custom CA certificate file (PEM format) for self-signed or enterprise PKI
ca_file = "/etc/niebla/tls/ca.crt"
# Expected server name override for TLS certificate validation (SNI)
server_name = "niebla.corp.internal"
```

---

### `[server]` (Central Daemon & Storage Engine)

Configures the `niebla-server` daemon, PostgreSQL database connection, and Content-Addressable Storage (CAS).

```toml
[server]
# Bind address for the niebla-server gRPC listener (0.0.0.0 listens on all interfaces)
host = "0.0.0.0"

# Port for the gRPC listener
port = 50051

# PostgreSQL connection URI (supports Unix domain sockets and TCP connections)
# Examples:
#   "postgres:///niebla?host=/run/postgresql"
#   "postgres://user:password@db.internal:5432/niebla?sslmode=verify-full"
db_url = "postgres:///niebla?host=/run/postgresql"

# Automatically spawn local PostgreSQL (via native pg_ctl or Docker) if not running on loopback
auto_db = true

# Local filesystem root path for content-addressable storage (CAS) blob store
# On high-throughput deployments, point this to an NVMe drive or ZFS dataset
blob_path = "/devpool/niebla_blobs"

# Maximum number of Merkle tree nodes cached in LRU memory
cache_size = 10000

# Optional list of authorized client bearer tokens. When non-empty, clients must supply auth_token
auth_tokens = [
  "sec_token_prod_eng_a8f9",
  "sec_token_ci_runner_3b1c"
]

[server.tls]
# Enable TLS encryption on the gRPC listener
enabled = false
# Server certificate file (PEM)
cert_file = "/etc/niebla/tls/server.crt"
# Server private key file (PEM)
key_file = "/etc/niebla/tls/server.key"
# Client CA bundle for enforcing mutual TLS (mTLS)
client_ca_file = "/etc/niebla/tls/client-ca.crt"
```

---

### `[fs]` (NieblaFS Mount & Chunk Caching)

Configures virtualized FUSE workspace mounts and local chunk caching.

```toml
[fs]
# Root mount directory for virtual FUSE workspaces (supports $HOME expansion)
mount_base = "$HOME/niebla/workspaces"

# Local cache directory for CAS blob chunks (supports $HOME expansion)
cache_dir = "$HOME/.cache/niebla/blobs"
```

---

### `[critique]` (Cotejo Web Review & Enterprise Governance)

Configures the Cotejo web review server, approval gates, comment threads, and persona classifications.

```toml
[critique]
# HTTP server bind address for the Cotejo web UI
host = "0.0.0.0"

# Port for the Cotejo web UI
port = 8080

# Live-reloading proxy port (used by developer watch modes)
proxy_port = 7331

# Default author/reviewer identity for comments and verdicts
default_reviewer = "$USER"

# Minimum number of formal approvals (LGTM / Approved) required before a CL can be submitted
# Defaults to 0 in local/tailscale mode, and 1 in multi-user/remote modes
required_approvals = 1

# When true, blocks 'niebla submit' if any comment thread remains unresolved (Open/In Progress)
allow_submit_with_unresolved_comments = false

# When true, blocks 'niebla submit' until all required CI check runs report SUCCESS
require_green_check_runs = false

# Glob patterns classifying author usernames as automated bots or AI review agents
# Bot comments receive distinct UI badges and are excluded from human approval quotas
agent_author_patterns = [
  "agent/*",
  "bot/*",
  "*[bot]",
  "*@agent*",
  "ci/*",
  "system"
]
```

#### `[critique.auth]` (Authentication & Identity)

```toml
[critique.auth]
# Identity resolution mode:
#   "local"     - Uses default_user or active OS username
#   "headers"   - Extracts identity from HTTP headers injected by reverse proxy / IdP
#   "tailscale" - Extracts verified Tailscale user login from WhoIs API
mode = "headers"

# Default fallback user identity
default_user = "$USER"

# Header name when mode = "headers"
# Examples: "X-Forwarded-User", "Cf-Access-Authenticated-User-Email", "X-Remote-User"
header_name = "X-Forwarded-User"

# List of trusted reverse proxy IP addresses or CIDR blocks permitted to inject identity headers
trusted_proxies = [
  "127.0.0.1/32",
  "10.0.0.0/8"
]

# When true, allows testing as different users via '?reviewer=<name>' (Strictly for dev/testing)
allow_query_override = false
```

#### `[critique.groups]` (Reviewer Group Aliases)

Allows assigning review groups (e.g., `@core-infra`, `@frontend`) to changelists.

```toml
[critique.groups]
# Group resolution mode: "static", "api", or "disabled"
mode = "static"

[critique.groups.static]
core-infra = ["alice", "bob", "carol"]
frontend = ["david", "elena"]
security = ["frank", "grace"]
```

#### `[critique.theme]` (Theming & Syntax Highlighting)

```toml
[critique.theme]
# Built-in presets:
#   "catppuccin-mocha"     - Dark pastel canvas (default)
#   "catppuccin-macchiato" - Medium dark canvas
#   "catppuccin-frappe"    - Low contrast matte dark
#   "catppuccin-latte"     - Crisp warm light mode
#   "tokyo-night"          - Cyberpunk indigo/blue
#   "dracula"              - Gothic purple
#   "nord"                 - Arctic blue
#   "github-dark"          - Monokai / GitHub dark
#   "custom"               - User-defined colors specified in [critique.theme.colors]
preset = "catppuccin-mocha"

# Chroma syntax highlighting style (matches preset or any registered Chroma style)
syntax_style = "catppuccin-mocha"

# Optional hex color overrides (supported with presets or required with preset = "custom")
[critique.theme.colors]
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
```

#### `[critique.ai]` (AI Review & Comment Hints)

Configures the backend inference endpoint that powers one-click code suggestions and AI hints in Cotejo.

```toml
[critique.ai]
# Master switch for AI comment hints in Cotejo UI
enabled = true

# OpenAI-compatible API base URL (works with OpenAI, vLLM, Ollama, LiteLLM, TensorRT-LLM)
endpoint = "https://api.openai.com/v1"

# Environment variable containing the API bearer token
api_key_env = "OPENAI_API_KEY"

# Model identifier
model = "gpt-4o"

# Request timeout in seconds
timeout_seconds = 30

# Sampling temperature (0.0 to 2.0; lower values yield deterministic code suggestions)
temperature = 0.2

# Path to system prompt markdown file (relative to workspace root or absolute)
system_prompt_path = ".niebla/prompts/comment_hint.md"

# Maximum line count allowed when widening context aperture from selected lines to full file
max_context_lines = 1200

# Name of the persona agent registered in .niebla/agents.toml
agent_name = "ai-hint-agent"

[critique.ai.tls]
# Enable TLS for LLM endpoint
enabled = false
# Path to custom CA certificate file (PEM)
ca_file = "/etc/niebla/tls/ca.crt"
# Server name override for TLS SNI validation
server_name = "llm.internal.corp"
```

---

### `[events]` (CloudEvents Notification Bus)

Publishes real-time changelist events (`niebla.cl.created`, `niebla.cl.updated`, `niebla.cl.submitted`) to external services.

```toml
[events]
# Master switch for event publishing
enabled = true

# Active event sink: "webhook", "exec", "log", or "noop"
sink = "webhook"

# Event filtering globs (default: all events)
filter = [
  "niebla.cl.*",
  "niebla.workspace.*"
]

# In-memory channel buffer capacity
buffer_size = 256

[events.webhook]
# HTTP POST endpoint destination
endpoint = "https://events.internal.corp/niebla"
# Environment variable holding the shared secret for HMAC signature verification
secret_token_env = "NIEBLA_EVENT_SECRET"
# HTTP delivery timeout in milliseconds
timeout_ms = 3000
# Maximum retry attempts on delivery failure
max_retries = 3

[events.exec]
# Command executed for each event; JSON payload is delivered via stdin
command = "/usr/local/bin/niebla-event-router.sh"
# Execution timeout in milliseconds
timeout_ms = 5000
```

---

### `[resilience]` (Disaster Recovery & Distributed Replication)

Governs high-availability replication, encrypted off-site archives, and scheduled backups.

```toml
[resilience]
# Master switch for automated backups and replication
enabled = true

# Primary replication mode: "zfs_stream" (recommended for 10GbE ZFS) or "bundle"
default_mode = "zfs_stream"

# ------------------------------------------------------------------------------
# Native ZFS Snapshot Streaming (10GbE Storage Network)
# ------------------------------------------------------------------------------
[resilience.zfs]
enabled = true
local_dataset = "zroot/data/niebla"
remote_host = "backup-node.internal"
remote_user = "niebla-repl"
remote_ssh_port = 22
remote_key_file = "$HOME/.ssh/id_ed25519"
remote_dataset = "backups/niebla"
# Raw encrypted send (-w) preserves native ZFS dataset encryption in-flight and at rest
raw_encrypted_send = true
snapshot_prefix = "niebla-auto"
retention_count = 30
bandwidth_limit_mb = 0 # 0 = unlimited wire speed

# ------------------------------------------------------------------------------
# Portable Encrypted Bundles (.nieblabak)
# ------------------------------------------------------------------------------
[resilience.bundle]
enabled = false
target_dir = "/mnt/backups/niebla"
compression = "zstd" # "zstd" or "gzip"
compression_level = 3 # 1-9

[resilience.encryption]
algorithm = "aes-256-gcm" # "aes-256-gcm" or "chacha20-poly1305"
key_source = "env"        # "env", "file", or "passphrase"
key_env = "NIEBLA_MASTER_KEY"
# key_file = "/etc/niebla/master.key"
# passphrase_env = "NIEBLA_BACKUP_PASSPHRASE"

# ------------------------------------------------------------------------------
# Cloud Object Storage (S3-Compatible: AWS, Cloudflare R2, MinIO, Ceph)
# ------------------------------------------------------------------------------
[resilience.s3]
enabled = true
endpoint = "s3.us-east-1.amazonaws.com"
bucket = "niebla-enterprise-backups"
region = "us-east-1"
prefix = "vcs/"
access_key_env = "AWS_ACCESS_KEY_ID"
secret_key_env = "AWS_SECRET_ACCESS_KEY"
force_path_style = false
use_ssl = true
storage_class = "STANDARD"

# ------------------------------------------------------------------------------
# WebDAV / Nextcloud Off-Site Destination
# ------------------------------------------------------------------------------
[resilience.nextcloud]
enabled = false
url = "https://nextcloud.internal.net/remote.php/dav/files/backup-user/niebla"
username = "backup-user"
password_env = "NEXTCLOUD_APP_PASSWORD"
timeout_seconds = 180

# ------------------------------------------------------------------------------
# Remote SFTP Streaming
# ------------------------------------------------------------------------------
[resilience.sftp]
enabled = false
host = "sftp.internal.net"
user = "niebla-sftp"
remote_path = "/storage/backups"
key_file = "$HOME/.ssh/id_ed25519"
timeout_seconds = 60

# ------------------------------------------------------------------------------
# Automated Schedule & Retention
# ------------------------------------------------------------------------------
[resilience.schedule]
auto_backup = true
interval_hours = 4
retention_count = 30
snapshot_on_submit = true # Takes an instant snapshot upon every successful SubmitCL
```

---

### `[ci]` (Orchestration & Verification Runners)

Configures automated CI check runs, pre-submit gating, and execution dispatchers.

```toml
[ci]
# Master switch for CI runner subsystem
enabled = true

# Default runner identifier to invoke if unspecified
default_runner = "local"

# When true, gates SubmitCL until all required check runs succeed
require_green_to_submit = true

# ------------------------------------------------------------------------------
# Local Runner (Sandboxed process execution on the server)
# ------------------------------------------------------------------------------
[ci.runners.local]
provider = "local"
work_dir = "/tmp/niebla-ci"
command = ["go", "test", "./..."]
timeout_seconds = 300
concurrency = 4

# ------------------------------------------------------------------------------
# Self-Hosted Forgejo / Gitea Actions Dispatcher
# ------------------------------------------------------------------------------
[ci.runners.forgejo-linux]
provider = "forgejo"
endpoint = "https://git.corp.internal/api/v1/repos/org/monorepo/actions/workflows/ci.yml/dispatches"
ref = "main"

[ci.runners.forgejo-linux.auth]
mode = "token"
token_env = "FORGEJO_TOKEN"

# ------------------------------------------------------------------------------
# GitHub Actions Dispatcher
# ------------------------------------------------------------------------------
[ci.runners.github-ci]
provider = "github"
endpoint = "https://api.github.com/repos/org/monorepo/dispatches"
event_type = "niebla_presubmit"

[ci.runners.github-ci.auth]
mode = "bearer"
token_env = "GITHUB_PAT"
```

---

## 5. CLI Configuration Management Suite (`niebla config`)

The `niebla config` command family provides tools for inspecting, validating, generating, and debugging configuration files.

| Command | Output / Action | Primary Use Case |
| :--- | :--- | :--- |
| `niebla config path` | Absolute filesystem path of active `config.toml`. | Locating config for scripts or editors. |
| `niebla config show` | Raw TOML contents of primary config file. | Quick terminal inspection. |
| `niebla config show --resolved` | Fully evaluated configuration after 3-way merge and env expansion. | Verifying dynamic `$USER` and modular file combinations. |
| `niebla config validate [file]` | Fast-fail semantic and syntactic verification. | Pre-flight validation in CI and hooks. |
| `niebla config init [--force]` | Generates canonical embedded baseline defaults. | Initializing new environments. |

### Inspecting Resolved Configuration

To verify that modular files (`config.toml`, `ai.toml`, `theme.toml`) are merging correctly and that environment variables are resolving properly, use `--resolved`:

```console
$ niebla config show --resolved
version = 1

[core]
server_addr = "127.0.0.1:50051"
default_workspace = "default"

[critique]
default_reviewer = "developer"
host = "0.0.0.0"
port = 8080
proxy_port = 7331

[critique.ai]
enabled = true
endpoint = "https://api.openai.com/v1"
model = "gpt-4o"

[critique.theme]
preset = "catppuccin-mocha"
syntax_style = "catppuccin-mocha"
```

---

## 6. Fast-Fail Validation & Error Diagnostics

Niebla runs a strict validation engine before starting any subsystem or accepting any workspace command. If any configuration invariant is violated, the process immediately halts with a clean diagnosis and actionable remediation hints.

### Built-In Invariants

1. **Schema Version:** `version` must be $\ge 1$.
2. **Port Collisions:** `critique.port`, `critique.proxy_port`, and `server.port` must be in range `[1, 65535]` and can **never** collide.
3. **Database URL:** `server.db_url` must begin with `postgres://`, `postgresql://`, or valid key-value connection string.
4. **Absolute Paths:** `fs.mount_base` must be an absolute path starting with `/`.
5. **Theme Presets:** `critique.theme.preset` must match one of the 9 supported presets. Typos trigger Levenshtein distance suggestions:
   ```
   ❌ Configuration validation failed:
     • [critique.theme.preset]: unknown theme preset "tokyonight"
       Hint: Did you mean "tokyo-night"? Supported presets: catppuccin-mocha, ...
   ```
6. **Color Syntax:** Any override under `[critique.theme.colors]` must follow valid `#RGB`, `#RRGGBB`, `#RRGGBBAA`, or `rgba(...)` formatting.
7. **Resilience Boundaries:**
   - `resilience.default_mode` must be either `"zfs_stream"` or `"bundle"`.
   - `resilience.encryption.algorithm` must be `"aes-256-gcm"` or `"chacha20-poly1305"`.
   - `resilience.postgres_replication.target_db_url` cannot be identical to the primary `server.db_url`.

### Pre-Flight Candidate Validation Pattern

When updating configuration programmatically or via AI coding agents, avoid editing `~/.config/niebla/config.toml` directly. Instead, use Niebla's pre-flight candidate validation pattern:

```bash
# 1. Write proposed configuration to a temporary candidate file
cat <<'EOF' > /tmp/candidate_config.toml
version = 1
[core]
server_addr = "127.0.0.1:50051"
[critique]
port = 8080
proxy_port = 8080 # Intentional collision to demonstrate validator
EOF

# 2. Run fast-fail pre-flight validation
niebla config validate /tmp/candidate_config.toml

# 3. Output indicates error:
# ❌ Configuration validation failed:
#   • [critique.proxy_port]: port 8080 collides with critique.port (8080)
#     Hint: proxy_port must be different from port to prevent listener collisions.

# 4. Once validated successfully (exit code 0), atomically copy into place
cp /tmp/candidate_config.toml ~/.config/niebla/config.toml
rm -f /tmp/candidate_config.toml
```

---

## 7. In-Repository Policy Configurations (`.niebla/*.toml`)

While `~/.config/niebla/` contains host-specific, user, and infrastructure parameters, repository governance rules reside directly inside the monorepo root under `.niebla/`. These files are tracked in version control:

- **`.niebla/OWNERS.toml`:** Defines subtree code owners, mandatory approval quotas, and path-based review requirements.
- **`.niebla/hooks.toml`:** Specifies client-side and server-side lifecycle hooks (e.g., `pre_submit`, `post_create`, `pre_sync`).
- **`.niebla/agents.toml`:** Registers automated AI review personas, context aperture widths, and evaluation prompts.

For a comprehensive guide on implementing custom in-tree review scripts, AI personas, CAS binaries, and self-hosted repository approval gates, see the [In-Tree Governance Guide](/niebla/workflows/in-tree-governance/).

By decoupling host and client connection settings (`~/.config/niebla/`) from versioned codebase policy (`.niebla/`), Niebla ensures that team security rules travel with the repository while individual developers retain full control over local mount paths and editors.

