---
title: Troubleshooting
description: Diagnose and recover from stale FUSE mounts, broken control sockets, stuck database advisory locks, and full CAS caches.
sidebar:
  order: 7
  label: Troubleshooting
---

This guide covers quick diagnostic commands and recovery steps for common workspace and server issues.

---

## Quick Reference Cheat Sheet

| Symptom | Diagnostic Command | Resolution |
| :--- | :--- | :--- |
| **Transport endpoint is not connected** | `ps aux \| grep nieblafs` | `fusermount3 -u -z <mount>` then `niebla workspace mount <name>` |
| **Device or resource busy on unmount** | `fuser -v -m <mount>` | Close shells/IDEs in `<mount>` or run `fuser -k -m <mount>` |
| **Control socket connection refused** | `ls -l ~/.niebla/workspaces/<name>/nieblafs.sock` | `rm -f ~/.niebla/workspaces/<name>/nieblafs.sock` then `niebla workspace mount <name>` |
| **Submit lock timeout** | Query `pg_locks WHERE locktype = 'advisory'` | `SELECT pg_terminate_backend(<pid>);` |
| **Local CAS cache disk full** | `niebla cas gc --dry-run` | `niebla cas gc --grace-period 24h` |

---

## 1. FUSE Virtual Filesystem Failures

### `Transport endpoint is not connected`
Occurs when the background `nieblafs` [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) daemon process was killed (`kill -9`, [OOM killer](https://en.wikipedia.org/wiki/Out_of_memory), or system reboot), leaving the kernel mount point detached. Your unshelved edits are stored in `~/.niebla/workspaces/<name>/scratch/` and are **not** lost.

```bash
# 1. Lazy-unmount the stale mount point
fusermount3 -u -z ~/niebla/workspaces/my-feature

# 2. Re-attach the nieblafs daemon
niebla workspace mount my-feature
```

### `Device or resource busy` During Unmount
A shell tab, editor, or language server still has open files or its working directory inside the workspace mount:

```bash
# List processes holding open handles inside the mount
fuser -v -m ~/niebla/workspaces/my-feature

# Terminate blocking processes if needed, then unmount
fuser -k -m ~/niebla/workspaces/my-feature
niebla workspace unmount my-feature
```

### Supported Filesystem Operations in FUSE Mounts
`nieblafs` mounts implement standard POSIX file operations:
* **Directory Operations:** `mkdir` (`fs.NodeMkdirer`), `rmdir` (`fs.NodeRemover`), and nested traversal.
* **File Operations:** File creation via `touch`, `cp`, or IDEs (`fs.NodeCreater`), in-place writes (`fs.HandleWriter`), and deletions (`rm` via `fs.NodeRemover`).
* **Renames:** Moving files or directories (`mv` via `fs.NodeRenamer`) within the workspace overlay.
* **Storage Location:** All added or modified files reside in `~/.niebla/workspaces/<name>/scratch/`. Deleting base tree files creates `.wh.<name>` whiteout markers in scratch without mutating CAS storage.

---

## 2. Stale UNIX Control Socket (`nieblafs.sock`)

If `niebla sync` warns `failed to re-anchor nieblafs daemon via control socket: connection refused`, a stale [UNIX domain socket](https://en.wikipedia.org/wiki/Unix_domain_socket) file was left behind after an unclean shutdown:

```bash
rm -f ~/.niebla/workspaces/my-feature/nieblafs.sock
niebla workspace mount my-feature
```

---

## 3. Stuck PostgreSQL Trunk Advisory Lock

`niebla-server` serializes trunk submissions using a transaction-scoped PostgreSQL advisory lock (`7955998246305947649`). If `niebla submit` times out waiting for the lock after a server crash, inspect active advisory locks in PostgreSQL:

```sql
SELECT l.pid, a.usename, a.client_addr, a.state, now() - a.query_start AS duration, a.query
FROM pg_locks l
JOIN pg_stat_activity a ON l.pid = a.pid
WHERE l.locktype = 'advisory'
ORDER BY duration DESC;
```

Terminate the stalled backend session if necessary:

```sql
SELECT pg_terminate_backend(<pid>);
```

---

## 4. Local CAS Cache Management & Pruning

To reclaim disk space in `~/.cache/niebla/blobs` ([Content-Addressable Storage](https://en.wikipedia.org/wiki/Content-addressable_storage)) without corrupting active workspaces, use the built-in [mark-and-sweep garbage collector](https://en.wikipedia.org/wiki/Tracing_garbage_collection#Basic_algorithm):

```bash
# Preview unreferenced blobs that can be removed
niebla cas gc --dry-run

# Delete unreferenced blobs older than 24 hours
niebla cas gc --grace-period 24h
```

:::caution[Do Not Manually `rm -rf` the CAS Cache While Workspaces Are Mounted]
Deleting cached blobs while `nieblafs` is running will cause open file reads to fail with `EIO`. Always use `niebla cas gc` or unmount workspaces first.
:::
