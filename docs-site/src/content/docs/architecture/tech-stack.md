---
title: System Architecture & Technology Stack
description: Low-level architectural breakdown of Niebla's multi-tiered monorepo toolchain, RPC protocols, persistence layers, and review engine.
---

Niebla is a server-authoritative, virtualized monorepo version control and code review toolchain. It is inspired by the design principles of Google's internal developer infrastructure: **Piper** (central monorepo repository daemon), **CitC** (*Client in the Cloud*, virtual FUSE-based workspaces), and **Critique** (patchset-oriented web code review).

This document outlines the system topology, technology choices, IPC/RPC protocols, data storage engines, and end-to-end transaction lifecycles that govern the Niebla ecosystem.

---

## 1. System Topology & Data Flow

Niebla separates filesystem mutation from canonical storage. Workstations never download entire repository packfiles or perform local graph traversals. Instead, workstations run lightweight virtual filesystem mounts, communicating with a central daemon over high-throughput gRPC.

```
                            DEVELOPER WORKSTATION
  ┌──────────────────────────────────────────────────────────────────────────┐
  │                                                                          │
  │  niebla CLI (Cobra POSIX client)                                         │
  │    │                                                                     │
  │    ├─► Workspace Lifecycle (create, sync, destroy)                       │
  │    ├─► Change Inspection (status, diff against base CL)                  │
  │    └─► Shelving & Patchsets (niebla shelve, niebla submit)               │
  │                                                                          │
  │  nieblafs (Virtual FUSE Daemon via bazil.org/fuse)                       │
  │    │                                                                     │
  │    ├─► Kernel VFS (/dev/fuse) ◄─── POSIX Syscalls (IDE / Compilers)      │
  │    ├─► In-Memory Metadata LRU (hashicorp/golang-lru/v2)                  │
  │    └─► Copy-on-Write Scratch Layer (~/.niebla/workspaces/<ws>/)          │
  │                                                                          │
  └─────────────────────────────────────┬────────────────────────────────────┘
                                        │
                                        │ gRPC over HTTP/2 (niebla.v1)
                                        │ Unary Metadata + Streaming Blobs
                                        ▼
                            NIEBLA BACKEND SERVER
  ┌──────────────────────────────────────────────────────────────────────────┐
  │                                                                          │
  │  niebla-server (Central Repository Daemon)                               │
  │    │                                                                     │
  │    ├──► NieblaService gRPC Server (Protobuf v3)                          │
  │    │      • GetTree / GetNode (Merkle tree directory lookups)            │
  │    │      • GetBlob / ReadBlob / UploadBlob (64KB streaming)             │
  │    │      • ShelveCL / SubmitCL / SyncCL (Rebase engine)                 │
  │    │                                                                     │
  │    ├──► Relational Metadata Store (PostgreSQL 16+)                       │
  │    │      • pgx/v5 connection pool (puddle/v2)                           │
  │    │      • Type-safe compiled queries via sqlc                          │
  │    │      • Tables: changelists, cl_patchsets, trees, blobs              │
  │    │      • Advisory xact locks for monotonic trunk submissions          │
  │    │                                                                     │
  │    └──► Content-Addressable Storage (pkg/cas)                            │
  │           • Portable relative fanout: ab/cd/<sha256>                     │
  │           • High-throughput chunk streaming (64KB buffers)               │
  │           • Optional zstd compression (klauspost/compress)               │
  │                                                                          │
  │  cotejo (Web Code Review Server - port 8080)                             │
  │    │                                                                     │
  │    ├──► Type-Safe HTML Components (github.com/a-h/templ)                 │
  │    ├──► Real-Time SSE Pipeline (cl_events Server-Sent Events)            │
  │    ├──► Intra-Line Word Diff Engine (alecthomas/chroma/v2)               │
  │    └──► AI Review Hints & Suggestion Patchsets                           │
  │                                                                          │
  └──────────────────────────────────────────────────────────────────────────┘
```

---

## 2. Technology Stack Breakdown

Each component within Niebla is built with modern, statically typed, and dependency-conscious technologies chosen for high concurrency, zero-runtime GC overhead where possible, and strict POSIX/VFS compliance.

### Core Language & Runtime
* **Go 1.26**: The entire toolchain (`niebla-server`, `nieblafs`, `niebla`, and `cotejo`) is written in Go. Go was selected for its native CSP concurrency primitives (`goroutines`, `channels`), first-class POSIX system call support (`golang.org/x/sys`), and robust cross-platform networking stack.
* **Hermetic Packaging via Nix**: Builds are managed declaratively using [`flake.nix`](file:///home/matatan/Developer/niebla/niebla/flake.nix). Toolchain binaries compile with zero external dynamic runtime library requirements on Linux and macOS.

### Virtual Filesystem (VFS) Layer
* **`bazil.org/fuse`**: A pure-Go FUSE library communicating directly with the Linux kernel `/dev/fuse` interface.
* **Kernel Cache Coordination**: Implements `fs.FS`, `fs.Node`, `fs.NodeStringLookuper`, `fs.HandleReadDirAller`, `fs.HandleReader`, and `fs.HandleWriter`. Virtual directory lookups and attributes (`getattr`, `readdir`) are served from local memory, while file bodies are streamed on-demand.
* **In-Memory Caching (`hashicorp/golang-lru/v2`)**: Thread-safe Least Recently Used (LRU) cache storing directory manifests, inode mappings, and hot CAS blob chunks.

### Inter-Process Communication & RPC Protocol
* **gRPC & Protocol Buffers (`niebla.v1`)**: All network communications between CLI clients, FUSE daemons, and `niebla-server` use HTTP/2 gRPC with Protobuf v3 definitions (`pkg/proto/v1/niebla.proto`).
* **Streaming Chunks**: Large file operations avoid in-memory buffering. Blob transfers use bidirectional or server-streaming RPCs (`stream BlobChunk`) with fixed `64KB` buffers (`DefaultChunkSize = 64 * 1024`).
* **Connection Pooling & Resiliency**: Client channels support automated backoff retry, keepalive pings, and transient failure recovery (`pkg/resilience`).

### Persistence & Relational Metadata
* **PostgreSQL 16+**: Canonical storage for monorepo history, changelists, patchset revisions, comments, approvals, and attention sets.
* **`jackc/pgx/v5` & `puddle/v2`**: High-performance PostgreSQL driver using binary format protocol and concurrent connection pooling.
* **Compile-Time Type Safety (`sqlc`)**: All SQL queries (`internal/db/queries/*.sql`) are compiled into type-safe Go structs and transaction methods (`internal/db/sqlc/`), eliminating ORM overhead and runtime reflection errors.
* **Concurrency Locking (`pg_advisory_xact_lock`)**: Submit operations serialize trunk revision assignments using database transaction-scoped advisory locks, guaranteeing strictly monotonic revision IDs without race conditions.

### Content-Addressable Storage (CAS)
* **Two-Level Prefix Fanout**: Blobs are addressed strictly by their cryptographic SHA-256 digest. File storage paths are partitioned using the first 4 characters of the hex string:
  ```text
  ${STORAGE_PATH}/ab/cd/abcdef0123456789...
  ```
  This prevents filesystem inode exhaustion in flat directories when storing millions of objects.
* **Atomic Writes via Temporary Buffers**: Uploads stream to a staging directory (`${STORAGE_PATH}/.tmp/<uuid>`). Upon SHA-256 verification, the file is atomically renamed into its final partitioned location.
* **Transparent Compression**: Compresses text and source code assets on disk using `klauspost/compress/zstd`.

### Code Review Engine (Cotejo)
* **Compiled HTML Templates (`a-h/templ`)**: Server-side rendered (SSR) templates compiled ahead-of-time into Go code. Provides zero-runtime template parsing, extreme rendering speeds, and compile-time type checking of all UI props.
* **Intra-Line Syntax Diffing (`alecthomas/chroma/v2` & `sergi/go-diff`)**: Performs syntax tokenization and character/word-level diff analysis to render Critique-style side-by-side and unified diffs.
* **Real-Time Reactivity (Server-Sent Events)**: Clients establish a lightweight HTTP event stream (`/events`). When reviews, inline comments, or AI suggestions are posted, DOM updates are pushed instantly without requiring heavyweight WebSocket protocols or full-page reloads.

### Configuration & Tooling
* **Strict TOML Engine (`pelletier/go-toml/v2`)**: Fast-fail configuration parsing for `~/.config/niebla/config.toml`, `.niebla/OWNERS.toml`, `.niebla/hooks.toml`, and `.niebla/agents.toml`.
* **Cobra CLI (`spf13/cobra`)**: Standard POSIX CLI interface with shell auto-completion and `--json` machine-readable output.
* **Integration Testing (`testcontainers/testcontainers-go`)**: Unit and functional tests spin up ephemeral, production-identical PostgreSQL containers inside Docker/Podman for hermetic test execution.

---

## 3. End-to-End Data Lifecycle

To understand how Niebla's components operate together, consider the lifecycle of an edit made by a developer:

```
[Developer File Edit]
        │
        ▼
1. VFS Write: Kernel routes syscalls to /dev/fuse.
   nieblafs intercepts write() -> Promotes file to Copy-on-Write (CoW) scratch layer:
   ~/.niebla/workspaces/<ws>/scratch/src/main.go
        │
        ▼
2. Inspection: Developer runs `niebla status`.
   niebla CLI scans the local CoW scratch layer, compares file hashes against
   the base changelist Merkle tree (cached from niebla-server), and reports modifications.
        │
        ▼
3. Shelving: Developer runs `niebla shelve -m "feat: updated auth handler"`.
   a. niebla CLI streams new/modified scratch blobs to niebla-server via UploadBlob gRPC.
   b. Server writes blobs to CAS: ab/cd/<sha256>.
   c. CLI computes new Merkle tree root hash and calls ShelveCL.
   d. PostgreSQL records new patchset (cl_patchsets), generates CL ID (e.g. CL 104),
      and records pending state.
        │
        ▼
4. Code Review: Reviewers open Cotejo at http://<server-host>:8080/cl/104.
   a. Cotejo retrieves base tree hash and patchset tree hash from PostgreSQL.
   b. Diffs are generated on-the-fly by streaming blob pairs from CAS and highlighting
      via Chroma v2.
   c. Reviewer posts inline comment with code suggestion.
   d. Server stores comment in cl_comments and broadcasts update via SSE.
        │
        ▼
5. Trunk Submission: Author runs `niebla submit --cl 104`.
   a. niebla-server acquires pg_advisory_xact_lock(1) to serialize trunk submission.
   b. Verifies all mandatory OWNERS.toml approvals and passing CI check runs.
   c. Advances trunk_revision_seq and marks CL status as 'submitted'.
   d. Cascade Rebase Engine: Server queries all active stacked child CLs whose parent_cl = 104,
      computes automated 3-way Merkle tree rebases, and notifies authors of clean merges or conflicts.
```

---

## 4. Next Steps & Related Deep Dives

* [The FUSE Virtual Filesystem (`nieblafs`) Internals](/niebla/architecture/fuse-internals/): Low-level documentation on kernel inode management, whiteout files, and streaming fetchers.
* [`niebla-server` Technical Manual](/niebla/components/server/): Database schema, CAS engine, and cascade rebasing engine.
* [`niebla` CLI Reference](/niebla/reference/cli/): Complete command reference, flags, and configuration resolution order.
* [Cotejo Review UI Architecture](/niebla/cotejo/overview/): Critique-inspired interface, attention sets, and real-time SSE stream.
