---
title: "CLI Command Reference"
description: Reference for the unified niebla CLI, including global flags, workspace lifecycle, shelving, trunk submission, CI execution, CAS operations, and backups.
sidebar:
  order: 1
---

The `niebla` CLI manages virtual [Copy-on-Write (CoW)](https://en.wikipedia.org/wiki/Copy-on-write) workspaces, streams file blobs from [Content-Addressable Storage (CAS)](https://en.wikipedia.org/wiki/Content-addressable_storage), and coordinates changelist reviews and [trunk](https://en.wikipedia.org/wiki/Trunk-based_development) submissions with `niebla-server`.

Every command supports human-readable terminal output by default and deterministic `--json` output for scripts, CI pipelines, and AI coding agents.

---

## 1. Global Flags & Configuration Precedence

All `niebla` subcommands accept the following global flags:

| Flag | Environment Variable | Default | Description |
| :--- | :--- | :--- | :--- |
| `--server <addr>` | `NIEBLA_SERVER` | `127.0.0.1:50051` | Target `niebla-server` [gRPC](https://en.wikipedia.org/wiki/GRPC) endpoint (`host:port`). |
| `--workspace <name>` | `NIEBLA_WORKSPACE` | Auto-detected | Explicit workspace name (bypasses directory traversal). |
| `--config-dir <dir>` | `NIEBLA_CONFIG_DIR` | `~/.config/niebla/workspaces` | Directory storing local workspace metadata files. |
| `--json` | `NIEBLA_JSON` | `false` | Emit structured JSON output to `stdout`. |

### Precedence Order

Runtime settings resolve in the following order (highest to lowest):

1. Explicit CLI flags (`--server 10.0.0.5:50051`)
2. Environment variables (`NIEBLA_SERVER`)
3. Workspace metadata (`.niebla/workspace.toml`)
4. User configuration files (`~/.config/niebla/*.toml`)
5. Built-in defaults (`127.0.0.1:50051`)

When invoked without `--workspace`, `niebla` walks up from the current working directory to locate `.niebla/workspace.toml`, allowing you to run commands from any subdirectory inside a workspace.

---

## 2. Workspace Lifecycle (`niebla workspace`)

### `niebla workspace create`

Creates and mounts a new virtual [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) workspace (or materializes files directly when `--no-fuse` is set).

```bash
niebla workspace create <name> [flags]
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--cl <id>` | `0` (trunk head) | Base changelist or trunk revision to branch from. |
| `--no-fuse` | `false` | Materialize files directly to disk without mounting FUSE. |
| `--path <subpath>` | `""` (full repo) | Restrict `--no-fuse` materialization to a specific directory subtree. |
| `--mount-dir <dir>` | `~/niebla/workspaces/<name>` | Custom destination directory for the workspace. |
| `--no-hooks` | `false` | Skip `post_create` hooks defined in `.niebla/hooks.toml`. |

```bash
# Create an instant FUSE workspace from trunk head
niebla workspace create feature-oauth

# Branch a stacked workspace from an in-flight changelist
niebla workspace create backend-api --cl 104

# Materialize a workspace without FUSE (macOS, Docker, or CI)
niebla workspace create ci-build --no-fuse --mount-dir /tmp/src
```

---

### `niebla workspace list`

Lists all registered local workspaces, their base CL revisions, mount paths, and FUSE daemon statuses.

```bash
niebla workspace list [--json]
```

---

### `niebla workspace mount` & `unmount`

Re-attaches or detaches the background `nieblafs` FUSE daemon for an existing workspace without deleting local scratch edits.

```bash
# Re-mount a workspace after a system reboot
niebla workspace mount <name>

# Unmount the FUSE filesystem and stop the background daemon
niebla workspace unmount <name>
```

---

### `niebla workspace delete`

Unmounts the workspace, stops its `nieblafs` daemon, and deletes its local scratch directory (aliases: `destroy`, `rm`).

```bash
niebla workspace delete <name> [flags]
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-f, --force` | `false` | Force deletion even if unshelved modifications exist in scratch storage. |

---

## 3. Change Inspection (`niebla status` & `niebla diff`)

### `niebla status`

Scans the local CoW scratch overlay (`O(modified files)`) and lists pending workspace changes against the base [Merkle tree](https://en.wikipedia.org/wiki/Merkle_tree).

```bash
niebla status [--json]
```

| Status Code | Meaning |
| :--- | :--- |
| `M` | Modified file in the local scratch overlay. |
| `A` | Newly created file in the workspace. |
| `D` | Deleted file (tracked via `.wh.<name>` [whiteout](https://en.wikipedia.org/wiki/Union_mount) marker). |

---

### `niebla diff`

Displays a syntax-highlighted unified diff comparing local scratch modifications against the base Merkle tree. Base blobs are fetched on demand from CAS.

```bash
# Diff all modified files in the workspace
niebla diff

# Diff a specific file or directory
niebla diff services/auth/handler.go
```

---

## 4. Changelist Shelving (`niebla shelve`)

Uploads modified scratch files to CAS, synthesizes an updated Merkle tree on `niebla-server`, and creates a new Changelist (CL) or appends a new Patchset ($N+1$) to the active CL for review in Cotejo.

```bash
niebla shelve -m "<description>" [flags]
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-m, --message <text>` | *(required)* | Changelist summary and description. |
| `--cl <id>` | Active workspace CL | Update a specific changelist by uploading Patchset $N+1$. |
| `--new-cl` | `false` | Force creation of a new CL even if the workspace tracks an active CL. |
| `--on-cl <id>` | `0` | Stack this changelist on top of parent changelist `<id>`. |
| `--reviewers <list>` | `""` | Comma-separated reviewer usernames or `@group` aliases. |

```bash
# Create a new CL and assign reviewers
niebla shelve -m "feat(auth): add OAuth2 token validation" --reviewers "alice,@sec-team"

# Upload a new patchset to the active CL after addressing review feedback
niebla shelve -m "fix: validate token expiration claim"

# Stack a child CL on top of CL 145
niebla shelve -m "feat(api): expose OAuth endpoints" --on-cl 145
```

---

## 5. Trunk Submission (`niebla submit`)

Runs local `pre_submit` hooks, verifies server-side governance gates (`OWNERS.toml` approvals, unresolved comments, and required CI check runs), and atomically commits the changelist to the linear trunk.

```bash
niebla submit [flags]
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--cl <id>` | Active workspace CL | Explicit changelist ID to submit. |
| `--no-verify` | `false` | Skip local client `pre_submit` verification hooks. |
| `--force` | `false` | Bypass non-blocking check runs (requires administrative permission). |

---

## 6. Workspace Synchronization (`niebla sync`)

Re-anchors the workspace's base Merkle tree to the latest trunk head (or updated parent CL) while preserving local unshelved scratch edits. In FUSE mode, `niebla sync` invalidates stale kernel [inodes](https://en.wikipedia.org/wiki/Inode) over `nieblafs.sock` so updated files appear immediately.

```bash
niebla sync [flags]
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--dry-run` | `false` | Preview incoming upstream changes and potential conflicts without modifying local state. |
| `--no-hooks` | `false` | Skip `post_sync` hooks defined in `.niebla/hooks.toml`. |
| `-f, --force` | `false` | Force sync even if uncommitted changes exist in `--no-fuse` mode. |

---

## 7. Stateless Tree Export (`niebla export`)

Materializes an immutable changelist or trunk revision into a target directory without registering a workspace or mounting FUSE. Designed for ephemeral CI runners and release packaging.

```bash
niebla export --dest <dir> [flags]
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--dest <dir>` | *(required)* | Target directory where files will be written. |
| `--cl <id>` | `0` (trunk head) | Changelist ID to export. |
| `--patchset <n>` | Latest patchset | Specific patchset number of `--cl` to export. |
| `--concurrency <n>` | `8` | Number of parallel CAS download workers. |
| `--cas-dir <dir>` | `""` | Local CAS directory; uses zero-copy [hardlinks](https://en.wikipedia.org/wiki/Hard_link) (`0444`) when available. |

```bash
# Materialize CL 145 using zero-copy hardlinks from a warm runner cache
niebla export --cl 145 --dest /ci/workspace --cas-dir ~/.cache/niebla/blobs
```

---

## 8. CI Orchestration (`niebla ci`)

### `niebla ci exec`

Wraps a build or test command, reports `running` to Cotejo at launch, streams console output into CAS, and reports `passed` or `failed` based on the command's exit code.

```bash
niebla ci exec --cl <id> --runner <name> [flags] -- <command> [args...]
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--cl <id>` | *(required)* | Target changelist ID. |
| `--runner <name>` | *(required)* | Runner identifier matching `OWNERS.toml` / `ci.toml` (e.g., `unit-tests`). |
| `--patchset <n>` | Latest patchset | Target patchset number. |
| `--external-url <url>` | `""` | Link to an external CI dashboard (e.g., Forgejo or GitHub Actions run). |

```bash
niebla ci exec --cl 145 --runner "go-unit-tests" -- go test -v ./...
```

---

### `niebla ci report`

Directly reports a check run status to Cotejo from custom CI scripts or webhooks.

```bash
niebla ci report --cl <id> --runner <name> --status <status> [flags]
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--cl <id>` | *(required)* | Target changelist ID. |
| `--runner <name>` | *(required)* | Runner identifier. |
| `--status <state>` | *(required)* | One of: `queued`, `running`, `passed`, `failed`, `canceled`, `skipped`. |
| `--patchset <n>` | Latest patchset | Target patchset number. |
| `--log-file <path>` | `""` | Path to a local log file to upload to CAS and attach to the check run. |
| `--url <url>` | `""` | External CI run URL. |

---

## 9. Configuration Management (`niebla config`)

Inspects, initializes, and validates the `~/.config/niebla/*.toml` configuration cascade. See [Configuration (`config.toml`)](/niebla/reference/configuration/) for the full schema.

| Subcommand | Flags | Description |
| :--- | :--- | :--- |
| `niebla config path` | — | Print paths to all active configuration files in load order. |
| `niebla config show` | `--resolved` | Print raw or fully merged configuration (with environment variables expanded). |
| `niebla config validate` | `[file]` | Validate [TOML](https://en.wikipedia.org/wiki/TOML) syntax, section uniqueness, port collisions, and theme presets. |
| `niebla config init` | `--force` | Generate a baseline `~/.config/niebla/config.toml` file. |

---

## 10. Low-Level CAS Operations (`niebla cas`)

Directly interacts with the [SHA-256](https://en.wikipedia.org/wiki/SHA-2) Content-Addressable Storage engine. Also used to upload hermetic review binaries (`type = "cas_binary"`) referenced in [`.niebla/agents.toml`](/niebla/workflows/in-tree-governance/#3-automated-review-agents-nieblaagentstoml).

```bash
# Upload a file or binary to CAS and print its SHA-256 digest
niebla cas put <file>

# Download a blob by its 64-character hex SHA-256 digest
niebla cas get <sha256> [--out <file>]

# Run mark-and-sweep garbage collection on unreferenced blobs
niebla cas gc [--grace-period 24h] [--dry-run] [--dir <path>]
```

---

## 11. Backups & Disaster Recovery (`niebla backup`)

Manages [ZFS](https://en.wikipedia.org/wiki/ZFS) snapshot replication and [AES-256-GCM](https://en.wikipedia.org/wiki/Galois/Counter_Mode) encrypted backup bundles. See [ZFS Backups & Disaster Recovery](/niebla/recipes/disaster-recovery/) for operational runbooks.

| Subcommand | Flags | Description |
| :--- | :--- | :--- |
| `niebla backup create` | `--mode zfs\|bundle`, `--upload s3` | Trigger a ZFS snapshot stream or create an encrypted `.nbk` bundle. |
| `niebla backup list` | `--target zfs\|s3\|filesystem` | List available recovery points across local and remote targets. |
| `niebla backup verify` | `<file>` | Verify bundle checksums and cryptographic signatures. |
| `niebla backup restore` | `<file>` | Restore PostgreSQL metadata and CAS blobs from a backup bundle. |
| `niebla backup prune` | `--target <driver>`, `--keep <n>` | Prune expired snapshots or bundles according to retention count. |

---

## 12. AI Agent Skills (`niebla skills`)

Synchronizes Niebla's bundled AI agent skills (`niebla`, `niebla-config`, `niebla-ci`, `niebla-review-agent`) across installed coding harnesses (Antigravity, Claude Code, Cursor, and `.agents`). See [AI Agent Skills](/niebla/reference/skills/) for details.

| Subcommand | Flags | Description |
| :--- | :--- | :--- |
| `niebla skills sync` | `--proactive`, `--hub <dir>` | Extract embedded skills to the local Hub and symlink all agent harnesses. |
| `niebla skills status` | `--hub <dir>` | Verify symlink health across installed agent harnesses. |
| `niebla skills export` | `--dir <path>` | Export embedded `SKILL.md` files into a custom directory. |
