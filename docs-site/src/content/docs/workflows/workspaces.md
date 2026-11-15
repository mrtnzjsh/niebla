---
title: Workspaces & Storage
description: How Niebla workspaces organize local files, minimize disk usage through shared CAS caching, and manage mounts.
sidebar:
  order: 1
  label: Workspaces & Storage
---

In Niebla, every feature, bugfix, or experiment gets its own isolated **workspace** rather than an in-place branch checkout. Because workspaces share a single [Content-Addressable Storage (CAS)](https://en.wikipedia.org/wiki/Content-addressable_storage) cache and only write modified files to disk, you can keep dozens of workspaces open side by side with minimal disk overhead.

---

## 1. Anatomy of a Workspace

A Niebla workspace is anchored to an immutable **Base Changelist** (`base_cl`) identified by its [Merkle root hash](https://en.wikipedia.org/wiki/Merkle_tree) (`root_tree_hash`). On your workstation, a workspace consists of five local paths:

| Layer | Default Path | Purpose |
| :--- | :--- | :--- |
| **Mount Directory** | `~/niebla/workspaces/<name>` | The working directory opened by your shell, compiler, and IDE. Backed by `nieblafs` in [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) mode or materialized files in `--no-fuse` mode. |
| **Scratch Overlay** | `~/.niebla/workspaces/<name>/scratch` | Stores only modified files, newly created files, and `.wh.*` [whiteout](https://en.wikipedia.org/wiki/Union_mount) deletion markers. |
| **Shared CAS Cache** | `~/.cache/niebla/blobs` | Global [SHA-256](https://en.wikipedia.org/wiki/SHA-2) blob cache (`ab/cd/<sha256>`) shared across **all** workspaces on the machine. |
| **Control Socket** | `~/.niebla/workspaces/<name>/nieblafs.sock` | [UNIX domain socket](https://en.wikipedia.org/wiki/Unix_domain_socket) used by the CLI for daemon health checks and `niebla sync` cache invalidation. |
| **Workspace Metadata** | `~/.config/niebla/workspaces/<name>.json` | Tracks the workspace ID, base CL number, root tree hash, mount path, and daemon PID. |

### Comparison with Git Working Trees

| Characteristic | Git Clones & Worktrees | Niebla Workspaces |
| :--- | :--- | :--- |
| **Creation Time** | Seconds to minutes | **< 100 ms** in FUSE mode |
| **Initial Disk Usage** | Gigabytes per clone/worktree | **< 100 KB** (grows only with edits and read files) |
| **Context Switching** | `git checkout` / `git stash` mutates files in place | Run multiple isolated workspaces concurrently |
| **File Fetching** | Eager history download | On-demand streaming (FUSE) or local CAS [hardlinks](https://en.wikipedia.org/wiki/Hard_link) (`--no-fuse`) |

---

## 2. Local Disk Usage

Creating a FUSE workspace (`niebla workspace create feature-auth`) consumes **under 100 KB** of disk space (the mount folder, empty scratch folder, metadata JSON, and log file).

As you work, disk space grows only for files you touch or read:
1. **Unopened Files:** Consume **0 bytes** on disk (only lightweight directory metadata is held in memory by `nieblafs`).
2. **Read Unmodified Files:** Cached once in `~/.cache/niebla/blobs/` and shared across every workspace on your machine.
3. **Modified or Added Files:** Stored in `~/.niebla/workspaces/<name>/scratch/`.
4. **Deleted Files:** Represented by a 0-byte `.wh.<filename>` marker in scratch.
5. **Build Outputs (`target/`, `node_modules/`):** Written to scratch and ignored by version control via `.nieblaignore`.

To reclaim space from older cached blobs that are no longer referenced by active workspaces, run `niebla cas gc` (see [Troubleshooting: CAS Cache Pruning](/niebla/workflows/troubleshooting/#4-local-cas-cache-management--pruning)).

---

## 3. Managing Workspaces

### Create a Workspace
```bash
# Default FUSE workspace anchored to latest trunk
niebla workspace create feature-auth

# Branch from a specific changelist at a custom mount path
niebla workspace create feature-auth --cl 42 --mount-dir ~/projects/my-auth
```

### List Active Workspaces
```bash
niebla workspace list
```

```text
NAME             BASE CL   STATUS            MOUNT DIR                                    PID
feature-auth     104       ACTIVE            /home/user/niebla/workspaces/feature-auth    4821
ci-integration   104       ACTIVE (NO-FUSE)  /home/user/niebla/workspaces/ci-run          0
```

### Mount & Unmount FUSE Workspaces
You can stop the background `nieblafs` daemon without losing your scratch edits, or re-mount a workspace after a system reboot:

```bash
# Detach FUSE mount and stop background daemon
niebla workspace unmount feature-auth

# Re-attach FUSE mount
niebla workspace mount feature-auth
```

### Delete a Workspace
```bash
niebla workspace delete feature-auth
```
Unmounts `nieblafs`, stops the daemon, and deletes the workspace scratch directory. If unshelved edits exist in scratch, pass `--force` to confirm deletion.

---

## Next Steps

* **[Development Lifecycle](/niebla/workflows/development-lifecycle/):** Checking status, diffing, shelving CLs, syncing with trunk, and stacking workspaces.
* **[Direct Materialization (`--no-fuse`)](/niebla/workflows/direct-materialization/):** Running workspaces on macOS or inside containers without `/dev/fuse`.
* **[IDE Setup](/niebla/workflows/ide-setup/):** Configuring VS Code, JetBrains IDEs, Neovim, and Emacs.
