---
title: NieblaFS (Virtual Filesystem)
description: How the nieblafs daemon integrates with Linux /dev/fuse, streams blobs on demand, manages Copy-on-Write scratch overlays, and handles CLI flags.
---

**NieblaFS** (`nieblafs`) is the userspace [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) daemon that mounts Niebla workspaces on Linux workstations. It exposes multi-gigabyte [monorepos](https://en.wikipedia.org/wiki/Monorepo) as local directories in under 100 milliseconds without downloading the full repository tree upfront.

---

## 1. Kernel-to-Userspace Interface (`/dev/fuse`)

[FUSE (*Filesystem in Userspace*)](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) is a Linux kernel module that routes filesystem calls (`open`, `read`, `write`, `stat`, `readdir`) from applications to an unprivileged userspace process via `/dev/fuse`.

```mermaid
flowchart TD
    App["Userspace Tools<br/><i>(Editor, Compiler, Shell)</i>"]
    
    subgraph VFS["Linux Kernel VFS"]
        VFS_CACHE["Inode &amp; Page Cache"]
        VFS_FUSE["FUSE Driver (/dev/fuse)"]
    end

    subgraph Daemon["nieblafs Daemon"]
        D_FS["VFS Nodes (Dir / File)"]
        D_META["LRU Metadata Cache"]
        D_FETCH["gRPC BlobFetcher"]
    end

    App -->|"POSIX Syscalls"| VFS
    VFS -->|"FUSE Protocol"| Daemon
```

When an application opens a file in a mounted workspace:
1. The Linux [Virtual File System (VFS)](https://en.wikipedia.org/wiki/Virtual_file_system) routes a lookup request through `/dev/fuse` to `nieblafs`.
2. `nieblafs` resolves the path against its [whiteout markers](https://en.wikipedia.org/wiki/Union_mount), local scratch folder, and in-memory [Merkle tree](https://en.wikipedia.org/wiki/Merkle_tree) metadata cache.
3. Unopened files consume **0 bytes** on disk until a process actually calls `read()`.

---

## 2. Three-Tier Workspace Storage

`nieblafs` combines three storage tiers into a single unified directory tree:

1. **Tier 1 — Base Merkle Tree (Read-Only):** The immutable snapshot of the repository at the workspace's base Changelist (CL).
2. **Tier 2 — In-Memory [LRU](https://en.wikipedia.org/wiki/Cache_replacement_policies#Least_recently_used_(LRU)) Metadata Cache:** Caches directory manifests (`NodeEntry`: name, mode, size, [SHA-256](https://en.wikipedia.org/wiki/SHA-2)) and synthetic 64-bit [inode](https://en.wikipedia.org/wiki/Inode) numbers so `ls`, `stat`, and tree walks do not make repeated [gRPC](https://en.wikipedia.org/wiki/GRPC) calls.
3. **Tier 3 — [Copy-on-Write (CoW)](https://en.wikipedia.org/wiki/Copy-on-write) Scratch Layer (Read-Write):** A local directory (`~/.niebla/workspaces/<name>/scratch/`) that stores all modified files, newly created files, and deletion markers.

---

## 3. On-Demand Blob Streaming & Singleflight Coalescing

When a process reads an unmodified file:

```mermaid
sequenceDiagram
    autonumber
    actor App as Application
    participant FUSE as nieblafs
    participant Cache as Local CAS (~/.cache/niebla/blobs)
    participant Server as niebla-server (:50051)

    App->>FUSE: read(offset, count)
    FUSE->>Cache: Check ab/cd/<sha256>
    alt Cache Hit
        Cache-->>FUSE: Read directly from local disk
    else Cache Miss
        FUSE->>Server: ReadBlobRequest(sha256, offset, count)
        Server-->>FUSE: stream BlobChunk (64KB chunks)
        FUSE->>Cache: Write verified blob to local CAS
    end
    FUSE-->>App: Return bytes to kernel
```

* **Local [CAS](https://en.wikipedia.org/wiki/Content-addressable_storage) Cache Hit:** If `~/.cache/niebla/blobs/ab/cd/<sha256>` already exists on disk (even if downloaded by a different workspace), `nieblafs` reads directly from local storage with zero network traffic.
* **Singleflight Coalescing:** If a compiler spawns 16 threads that read the same uncached file simultaneously, `nieblafs` coalesces them with `singleflight.Group` so only a single gRPC stream is requested from `niebla-server`.

---

## 4. Directory & File Mutations (CoW Scratch Overlay)

NieblaFS directory nodes implement full FUSE mutation interfaces (`fs.NodeMkdirer`, `fs.NodeCreater`, `fs.NodeRemover`, `fs.NodeRenamer`), directing all workspace changes to the local Copy-on-Write (CoW) scratch layer without modifying immutable base Merkle trees.

```mermaid
flowchart TD
    VFS["Kernel VFS Call"] --> Op{"Operation Type"}

    Op -->|"mkdir"| Mkdir["Mkdir(relPath)<br/>os.Mkdir in scratch<br/>Clear negative cache"]
    Op -->|"open/create"| Create["Create(relPath, mode)<br/>os.OpenFile(O_CREATE) in scratch<br/>Clear negative cache"]
    Op -->|"rm / rmdir"| Remove{"Target Origin?"}
    Op -->|"rename"| Rename["Rename(old, new)<br/>os.Rename in scratch<br/>Whiteout old if in base<br/>Invalidate &amp; update caches"]

    Remove -->|"Scratch Added"| DelScratch["os.Remove(scratchPath)"]
    Remove -->|"Base Merkle Tree"| Whiteout["Write .wh.&lt;name&gt; in scratch<br/>Set negative cache (5s)"]
```

### 1. Directory Creation (`Mkdir`)
When `mkdir` is executed inside a virtual mount:
1. `nieblafs` determines the relative path from the current directory node.
2. It ensures parent directories exist in `~/.niebla/workspaces/<name>/scratch/`.
3. It creates the physical directory with requested mode permissions in the scratch layer.
4. Any prior whiteout marker (`.wh.<name>`) at that location is removed.
5. In-memory negative metadata cache entries for the path are cleared so subsequent lookups succeed immediately.

### 2. File Creation (`Create`)
When editors, language servers, or shell commands create new files:
1. `nieblafs` ensures parent directories exist in scratch.
2. It initializes the file in scratch using `os.OpenFile(..., os.O_CREATE|os.O_RDWR|os.O_TRUNC, mode)`.
3. Any prior whiteout marker is pruned.
4. The path's negative lookup cache is cleared.
5. It returns a `*File` node configured with `isScratch: true`, satisfying both `fs.Node` and `fs.Handle` for immediate streaming reads and writes.

### 3. File & Directory Deletion (`Remove`)
Deleting files or folders handles scratch edits and immutable base trees differently:
* **Scratch-Only Entries (`ChangeAdded`):** The file or empty directory is physically deleted from the scratch directory with `os.Remove`.
* **Base Merkle Entries (`ChangeDeleted`):** Because base blobs are immutable, `nieblafs` writes an [OverlayFS](https://en.wikipedia.org/wiki/OverlayFS)-compatible prefix whiteout marker:
  ```text
  ~/.niebla/workspaces/<name>/scratch/path/to/.wh.filename.go
  ```
  `Lookup` detects `.wh.*` and immediately returns `ENOENT` to prevent falling back to CAS blobs. `ReadDirAll` filters out whiteout files while suppressing the deleted entry from base directory listings.
* **Negative Cache Eviction:** `Remove` invalidates any cached metadata and sets a negative lookup TTL (`5s`) to prevent redundant gRPC inquiries.

### 4. File & Directory Renames (`Rename`)
When moving or renaming entries within the mount:
* If the file was created or modified in scratch, `nieblafs` moves the scratch path via `os.Rename`.
* If the source existed only in the base tree, `nieblafs` promotes the content into scratch at the new destination path and writes a `.wh.<oldName>` marker at the source location.
* Prior whiteout markers at the destination path are cleared.
* Negative and positive metadata cache entries are invalidated and refreshed.

---

## 5. Copy-on-Write (CoW) File Promotion
When a process opens an existing base file for modification:
1. `nieblafs` creates the parent directory hierarchy in `~/.niebla/workspaces/<name>/scratch/`.
2. It streams or copies the base blob into scratch before applying the write.
3. Subsequent reads and writes route directly to the local scratch file.

---

## 6. `nieblafs` Daemon CLI Reference

`nieblafs` is normally spawned and supervised automatically by `niebla workspace create` and `niebla workspace mount`, but it can also be invoked directly:

```bash
nieblafs --mount <dir> --scratch <dir> --server <host:port> [flags]
```

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--mount <path>` | *(Required)* | Directory where the virtual FUSE filesystem is mounted. |
| `--scratch <path>` | *(Required)* | Local directory for modified, added, and `.wh.*` whiteout files. |
| `--server <addr>` | `127.0.0.1:50051` | Address of the `niebla-server` gRPC instance. |
| `--cache <path>` | `~/.cache/niebla/blobs` | Shared local CAS cache directory for downloaded blobs. |
| `--socket <path>` | *(Optional)* | [UNIX domain socket](https://en.wikipedia.org/wiki/Unix_domain_socket) (`nieblafs.sock`) used by `niebla` for health checks and `niebla sync` re-anchoring. |
| `--debug` | `false` | Enables verbose FUSE operation logging to stderr. |

### Unmounting & Recovery
To detach a workspace cleanly without deleting scratch edits, run `niebla workspace unmount <name>` (or `fusermount3 -u <mount-path>`). If a daemon process crashes and leaves a stale mount point, see the [Troubleshooting Guide](/niebla/workflows/troubleshooting/#1-fuse-virtual-filesystem-failures).

For environments without `/dev/fuse` (such as macOS or unprivileged CI containers), see [Direct Materialization Mode (`--no-fuse`)](/niebla/workflows/direct-materialization/).
