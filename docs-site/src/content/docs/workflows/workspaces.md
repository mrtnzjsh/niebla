---
title: Virtual Workspaces
description: Managing on-demand FUSE virtual checkouts and Copy-on-Write overlays.
---

In Niebla, you do not switch branches in a single directory. Instead, each feature or task receives its own lightweight virtual workspace.

## Creating a Workspace

```bash
niebla workspace create feature-auth
```

By default, Niebla mounts a virtual FUSE filesystem backed by `nieblafs`:
- **Mount Directory:** `~/niebla/workspaces/feature-auth`
- **Scratch Directory:** `~/.niebla/workspaces/feature-auth/scratch` (or `/scratch/...`)
- **Base Changelist:** Inherited from latest trunk revision

---

## Listing Workspaces

```bash
niebla workspace list
```

Outputs active workspaces, base changelists, daemon PIDs, and mount health.

---

## Direct Materialization Mode (`--no-fuse`)

For CI environments, Docker containers, or macOS hosts where FUSE kernel drivers are not available:

```bash
niebla workspace create ci-run --no-fuse
```

In `--no-fuse` mode, files are materialized directly onto disk while still tracking base CL hashes.

