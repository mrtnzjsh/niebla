---
title: "Offline & Disconnected Monorepo Workflows"
description: Complete guide to working on laptop workstations during flights, remote travel, or network outages without active connectivity to niebla-server.
sidebar:
  order: 7
  label: Offline Monorepo Development
---

Because Niebla virtual workspaces use lazy on-demand streaming over HTTP/2 gRPC to materialize file blobs as they are opened, developers frequently ask: **"Can I work on an airplane without internet access?"**

The answer is **yes**. With appropriate local cache warming and understanding of Niebla's local Copy-on-Write (CoW) delta model, you can edit, build, test, diff, and inspect monorepo projects completely offline.

---

## 1. Offline Capabilities Matrix

| Operation | Offline Support | Mechanism & Notes |
| :--- | :---: | :--- |
| **Reading Cached Files** | ✅ **Full** | Blobs already present in `~/.cache/niebla/blobs` are served from local NVMe storage at native disk speed. |
| **Reading Uncached Files** | ⚠️ **Requires Cache** | Fails with `EIO` unless pre-warmed into local CAS prior to disconnecting. |
| **Modifying Existing Files** | ✅ **Full** | Writes are directed to your local scratch directory (`~/.niebla/workspaces/<name>/scratch/`). |
| **Adding / Deleting Files** | ✅ **Full** | New files and `.wh.*` deletion whiteouts are stored entirely in local scratch. |
| **Building & Running Tests** | ✅ **Full** | Compilers (`go build`, `cargo build`, `bazel`) and local test suites run normally against scratch and cached files. |
| **`niebla status`** | ✅ **Full** | Evaluates local scratch files against the workspace's base Merkle tree without server queries. |
| **`niebla diff`** | ✅ **Full** | Computes intra-line word diffs using locally cached base blobs. |
| **`niebla workspace create`** | ❌ **Requires Server** | Allocating new workspace IDs and root trees requires dialing `niebla-server`. |
| **`niebla sync`** | ❌ **Requires Server** | Checking upstream parent/trunk movement requires gRPC connectivity. |
| **`niebla shelve`** | ❌ **Requires Server** | Uploading new blobs and recording patchsets requires `niebla-server`. |

---

## 2. Pre-Flight Preparation: Warming the Local CAS Cache

Before boarding a flight or disconnecting from your corporate network, warm your local CAS blob cache for the subtrees or services you plan to work on:

```bash
# 1. In Direct Materialization mode (--no-fuse):
# Files are already 100% materialized on disk. Zero cache warming required!

# 2. In FUSE Virtual Mode:
# Recursively touch or build your target packages to populate local CAS:
cd ~/niebla/workspaces/feature-auth

# Eagerly stream all files in your service subtree:
find services/auth pkg/crypto -type f -exec cat {} + > /dev/null

# Or trigger your standard build/test suite while still online:
go test ./services/auth/...
```

Once a blob has been streamed into `~/.cache/niebla/blobs/`, it is immutable and retained on disk indefinitely until explicitly pruned via `niebla cas gc`.

---

## 3. Developing While Disconnected

While offline, your day-to-day coding loop functions identically to standard local Git workflows:

```bash
# 1. Edit code in your IDE or editor
$EDITOR services/auth/login.go

# 2. Add new test files
touch services/auth/passkey_test.go

# 3. Inspect working copy status (runs 100% locally)
niebla status
# Output:
#   Workspace: feature-auth
#   Base CL  : 104
#   Clean    : false
#   Changes  :
#     [M] services/auth/login.go
#     [A] services/auth/passkey_test.go

# 4. Review unified diffs locally
niebla diff
```

---

## 4. Reconnecting & Synchronizing Upstream

When you regain internet access:

1. **Verify Server Reachability**:
   ```bash
   niebla config path
   ```
2. **Synchronize Base Tree (`niebla sync`)**:
   Pull down any changes that landed on trunk while you were disconnected:
   ```bash
   niebla sync
   ```
   - If disjoint changes occurred, `niebla` fast-forwards your base tree cleanly.
   - If overlapping file changes occurred, `niebla` injects standard conflict markers (`<<<<<<<`) into your scratch files for interactive editing.
3. **Shelve Your Work for Review (`niebla shelve`)**:
   Once tests pass, upload your offline modifications to central CAS and create a changelist:
   ```bash
   niebla shelve -m "feat(auth): add passkey authentication support"
   ```
