---
title: Offline Development
description: How to warm the local CAS cache and work on a Niebla monorepo without network connectivity.
sidebar:
  order: 6
  label: Offline Development
---

Because Niebla caches file blobs in `~/.cache/niebla/blobs` and tracks edits locally in your workspace scratch directory, you can edit, build, test, and inspect diffs completely offline.

---

## 1. What Works Offline

| Operation | Offline Support | Notes |
| :--- | :---: | :--- |
| **Read cached files** | ✅ Yes | Served directly from `~/.cache/niebla/blobs`. |
| **Read uncached files ([FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace))** | ⚠️ Needs cache | Returns `EIO` unless pre-warmed into local [CAS](https://en.wikipedia.org/wiki/Content-addressable_storage) before disconnecting. |
| **Edit, add, or delete files** | ✅ Yes | Stored locally in `~/.niebla/workspaces/<name>/scratch/`. |
| **Compile & run local tests** | ✅ Yes | Runs against scratch files and cached CAS blobs. |
| **`niebla status` & `niebla diff`** | ✅ Yes | Compares local scratch edits against cached base blobs. |
| **`workspace create` / `sync` / `shelve`** | ❌ Needs server | Requires [gRPC](https://en.wikipedia.org/wiki/GRPC) connection to `niebla-server`. |

---

## 2. Warming the Cache Before Disconnecting

* **In Direct Materialization Mode (`--no-fuse`):** All files in your workspace are already materialized on disk—no extra steps are needed.
* **In FUSE Mode:** Before going offline, run your build/test suite or read the directories you plan to work on so `nieblafs` populates `~/.cache/niebla/blobs`:

```bash
cd ~/niebla/workspaces/feature-auth

# Stream all files in target subdirectories into the local CAS cache
find services/auth pkg/crypto -type f -exec cat {} + > /dev/null

# Or run your test suite to cache all transitive dependencies
go test ./services/auth/...
```

Cached blobs remain in `~/.cache/niebla/blobs` until explicitly pruned with `niebla cas gc`.

---

## 3. Working Offline & Reconnecting

While offline, edit files and check your changes normally with `niebla status` and `niebla diff`.

Once you regain connectivity:
1. Run `niebla sync` to pull any upstream trunk commits that landed while you were offline (and resolve any conflicts if needed).
2. Run `niebla shelve -m "..."` to upload your modified blobs and publish a changelist.
