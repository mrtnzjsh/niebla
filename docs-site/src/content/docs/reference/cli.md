---
title: CLI Command Reference
description: Complete synopsis of all subcommands and global flags in the niebla CLI.
---

The `niebla` command-line interface communicates with `niebla-server` via gRPC.

## Global Flags

| Flag | Environment Variable | Description |
| :--- | :--- | :--- |
| `--server` | `NIEBLA_SERVER` | gRPC server endpoint (defaults to `127.0.0.1:50051`) |
| `--workspace` | `NIEBLA_WORKSPACE` | Explicit workspace name (overrides `$PWD` detection) |
| `--config-dir` | `NIEBLA_CONFIG_DIR` | Custom directory for workspace metadata |
| `--json` | `NIEBLA_JSON` | Emit structured JSON output for AI agents and automation |

---

## Subcommands

- `niebla workspace create <name>`: Create and mount a virtual workspace.
- `niebla workspace list`: List active workspaces and health metrics.
- `niebla status`: Inspect modified, added, and whiteout-deleted files in scratch overlay.
- `niebla diff`: Display intra-line syntax-highlighted diffs against base changelist.
- `niebla shelve`: Snapshot scratch layer and create a new patchset or changelist.
- `niebla submit`: Evaluate OWNERS policies and commit changelist to trunk.
- `niebla sync`: Update base changelist of current workspace to latest trunk.
- `niebla export`: Export an immutable snapshot of the current workspace tree.

