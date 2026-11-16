# Niebla: Unified Monorepo & Code Review Platform

[![Documentation](https://img.shields.io/badge/docs-mrtnzjsh.github.io%2Fniebla-blue?style=flat-square)](https://mrtnzjsh.github.io/niebla/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg?style=flat-square)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![NixOS Module](https://img.shields.io/badge/NixOS-Module%20Ready-5277C3?style=flat-square&logo=nixos)](https://nixos.org)

**Niebla** is an open-source, server-authoritative monorepo version control and code review platform. 

It is engineered from the ground up to eliminate the friction, clone bloat, and rebase sprawl inherent in distributed Git workflows—scaling effortlessly from **an individual developer hacking on a single laptop** to **fast-moving medium-sized engineering organizations** running distributed CI pipelines and autonomous AI coding swarms.

---

## The Vision: Monorepo Simplicity for Teams of Any Size

As codebases grow, traditional distributed version control systems begin to degrade: downloading gigabytes of historical Git packfiles slows onboarding, branch switching pollutes local filesystems, merge skew causes silent regressions, and pull request inboxes drown in noisy notifications.

Niebla solves this by treating the monorepo as a **central, high-throughput service**:

1. **Zero-Clone Virtual Workspaces:** Workspaces provision in under 15 milliseconds. File trees are virtualized via FUSE (`nieblafs`) or materialized directly (`--no-fuse`), streaming file bodies on-demand and deduplicating blobs across all workspaces in a shared Content-Addressable Storage (CAS) cache.
2. **Instant Change Detection:** You never wait for `status` to scan millions of clean files. Niebla tracks edits in a private Copy-on-Write (CoW) scratch layer, calculating status and diffs in $O(\text{changed})$ time.
3. **Linear Patchsets Over Commit Soup:** Work is shelved into server-authoritative Changelists (CLs). Reviews progress through immutable patchsets with inter-diff comparisons, eliminating merge commits and branch spaghetti.
4. **Actionable Code Review (Cotejo):** Review code in a blazing-fast web interface featuring intra-line word diffs, attention set state machines, and one-click AI code suggestion application.
5. **Human & Agent Coexistence:** Designed for humans, but optimized with deterministic `--json` contracts, granular `OWNERS.toml` governance gates, and dedicated skill adapters for AI coding assistants.

---

## System Architecture

```mermaid
flowchart TD
    subgraph Workstation["Developer Workstation"]
        CLI["niebla CLI"]
        FS["nieblafs FUSE Daemon"]
        Direct["Direct Materialization"]
        Scratch["Local Scratch Overlay"]
        CASLocal["Local CAS Blob Cache"]

        CLI --> FS
        CLI --> Direct
        FS --> Scratch
        FS --> CASLocal
    end

    subgraph Server["Repository Server"]
        Daemon["niebla-server Daemon"]
        CASRemote["Central CAS Storage"]
        DB[("PostgreSQL Database")]
        Cascade["Cascade Rebase Engine"]

        Daemon --> CASRemote
        Daemon --> DB
        Daemon --> Cascade
    end

    subgraph Review["Cotejo Review UI"]
        Cotejo["Cotejo Web Service"]
        SSE["Server-Sent Events Stream"]
        Attention["Attention Set Tracker"]
        Agents["AI Review Personas"]

        Cotejo --> SSE
        Cotejo --> Attention
        Cotejo --> Agents
    end

    CLI -->|"gRPC Protocol"| Daemon
    FS -.->|"Blob Streaming"| CASRemote
    CLI -->|"Review URL"| Cotejo
    Cotejo -->|"Query Revisions"| Daemon
```

---

## Key Capabilities

### 1. Virtual Workspaces (`niebla workspace`)
Never clone gigabytes of packfiles or juggle stashes. Each feature, bugfix, or experiment receives its own lightweight workspace:
* **FUSE Mode (Default):** Virtual mount point backed by `nieblafs`. Full POSIX mutation support (`mkdir`, `touch`, file creation, `rm`, `mv`) routing changes seamlessly to a local Copy-on-Write (CoW) scratch overlay while virtualizing clean files. Storage on creation is virtually zero (< 100 KB).
* **Direct Materialization (`--no-fuse`):** Optimized for Windows (native `niebla.exe`), macOS (Apple Silicon & Intel APFS), Docker containers, Kubernetes pods, CI runners, and environments where `/dev/fuse` is unavailable. Uses parallel streaming and local caching for fast checkouts with automatic backslash and Unicode NFC normalization.
* **Mount Management:** Cleanly attach, detach, or recover workspace mounts with `niebla workspace mount` and `unmount`.

### Cross-Platform Client Matrix

| Operating System | Supported Mode | Executable | Workspace Creation Command | Client Capabilities & Semantics |
| :--- | :--- | :--- | :--- | :--- |
| **Linux (NixOS, Ubuntu, RHEL)** | FUSE (Default) & `--no-fuse` | `niebla`, `nieblafs` | `niebla workspace create <name>` | Zero-clone FUSE virtual mount backed by local CoW scratch overlay. |
| **macOS (Darwin ARM64 / M-Series & Intel)** | Direct (`--no-fuse`) | `niebla` | `niebla workspace create <name> --no-fuse --server <addr>` | Direct APFS materialization. Decomposed Unicode (NFD) automatically normalized to NFC. |
| **Windows (10, 11, Server x64)** | Direct (`--no-fuse`) | `niebla.exe` | `niebla.exe workspace create <name> --no-fuse --server <addr>` | Standalone Windows binary (cmd/PowerShell). Backslashes normalized; NTFS case collisions safely intercepted. |
| **Windows WSL2** | FUSE & `--no-fuse` | `niebla`, `nieblafs` | `niebla workspace create <name>` | Full Linux capabilities within WSL2 environment. |

### 2. Instant Change Tracking & Shelving
* **Fast Status:** Only modified files, newly added files, or whiteout deletion markers (`.wh.*`) occupy disk space in scratch. `niebla status` and `niebla diff` run instantaneously regardless of total monorepo size.
* **Changelist Shelving:** `niebla shelve -m "..."` snapshots local edits into a new Merkle tree, uploads chunks to CAS in 64KB streams, and generates an immutable patchset with a direct Cotejo review URL.

### 3. Cotejo: High-Performance Code Review
* **Two-Pass Intra-Line Diff Engine:** Line-level LCS diffs paired with character- and word-level slicing, retaining language syntax highlighting inside diff additions and deletions.
* **Inter-Patchset Diffs:** Compare **Patchset 1 vs. Patchset 2** to review *only* the new changes made in response to feedback.
* **The Attention Set:** Automatically tracks whose turn it is to act (Author vs. Reviewers), preventing reviews from stalling in limbo.
* **One-Click Patch Application:** Reviewers or AI agents can provide replacement code suggestions; authors apply them with a single click (`✓ Apply Suggestion`), synthesizing a new patchset automatically.
* **Vim Keyboard Ergonomics:** Rapid file navigation (`j`/`k`), jump to comments (`n`/`p`), and toggle reviewed files (`m`).

### 4. Monotonic Trunk & Stacked Development
* **Strict Linear History:** Submissions serialize via database advisory locks (`pg_advisory_xact_lock`), incrementing a monotonic revision counter ($r1, r2, r3\dots$).
* **Stacked Changelists:** Branch one feature from another using `niebla workspace create child --cl <parent_id>`.
* **Automated Cloud Cascades:** When a parent CL commits to trunk, Niebla's background cascade engine automatically rebases dependent child changelists in the cloud.

### 5. Enterprise Resilience & Disaster Recovery
* **CAS Garbage Collection:** Mark-and-sweep GC (`niebla cas gc`) reclaims unreferenced blobs older than a configurable grace period.
* **Envelope-Encrypted Archives:** `niebla backup create --encrypt` produces standalone `.nieblabak` bundles encrypted with AES-256-GCM or ChaCha20-Poly1305.
* **10GbE ZFS Snapshot Replication:** Native block-level streaming for zero-downtime hot standbys and disaster recovery cutover.

### 6. AI Agent Harness Integration
Niebla ships with built-in skill adapters for modern AI coding environments:
* **Google Antigravity, Claude Code, Cursor, and Universal `.agents`:** Run `niebla skills sync` to link all 4 skills (`niebla`, `niebla-ci`, `niebla-config`, `niebla-review-agent`) across your workstation harnesses.

---

## Quickstart

### Option A: Declarative NixOS Deployment (Recommended)

If you are running **NixOS**, the repository provides a complete, self-bootstrapping NixOS module.

1. Add Niebla to your system flake inputs:
   ```nix
   inputs.niebla.url = "github:mrtnzjsh/niebla";
   ```

2. Enable the service in your NixOS configuration:
   ```nix
   {
     services.niebla.enable = true;
   }
   ```

3. Run `sudo nixos-rebuild switch`.  
   **NixOS automatically handles everything:** compiles the binaries, launches an isolated PostgreSQL instance on port `5433`, applies database migrations, seeds Depot Root (CL 1), starts `niebla-server.service`, and boots `cotejo.service` on port `8080`.

---

### Option B: Standalone Manual Installation

#### 1. Compile the Binaries
Niebla is written in pure Go and builds with zero CGO dependencies:
```bash
cd niebla
CGO_ENABLED=0 go build -o /usr/local/bin/niebla ./cmd/niebla
CGO_ENABLED=0 go build -o /usr/local/bin/niebla-server ./cmd/niebla-server
CGO_ENABLED=0 go build -o /usr/local/bin/nieblafs ./cmd/nieblafs
CGO_ENABLED=0 go build -o /usr/local/bin/cotejo ./cmd/cotejo
```

#### 2. Start PostgreSQL & Background Services
```bash
# Run local PostgreSQL (or connect to an existing cluster)
docker run -d --name niebla-pg -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:16-alpine

# Start repository daemon & web review server
niebla-server --db-url "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable" &
cotejo --db-url "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable" &
```

---

### The Developer Workflow in 60 Seconds

```bash
# 1. Create and enter a new virtual workspace
niebla workspace create feature-api
cd ~/niebla/workspaces/feature-api

# 2. Make your modifications
echo "package main" > main.go
niebla status
niebla diff

# 3. Shelve into a Changelist for review
niebla shelve -m "feat(api): initialize core service endpoint"
# Output prints review URL: http://127.0.0.1:8080/cl/2

# 4. Open Cotejo in your browser, review diffs, and approve
# Navigate to http://127.0.0.1:8080/cl/2

# 5. Land the change onto trunk
niebla submit --cl 2
```

---

## Documentation

Comprehensive documentation, API specs, and runbooks are available at the **[Niebla Documentation Site](https://mrtnzjsh.github.io/niebla/)**:

### Getting Started & Concepts
* **[Architectural Philosophy](https://mrtnzjsh.github.io/niebla/getting-started/philosophy/):** Why server-authoritative Merkle trees outperform distributed packfiles at scale.
* **[Quickstart Walkthrough](https://mrtnzjsh.github.io/niebla/getting-started/quickstart/):** Step-by-step tutorial from workspace creation to trunk submission.
* **[Workspaces & Storage Economics](https://mrtnzjsh.github.io/niebla/workflows/workspaces/):** Five-layer workstation architecture and disk consumption benchmarks.

### Workflows & Guides
* **[The Development Lifecycle](https://mrtnzjsh.github.io/niebla/workflows/development-lifecycle/):** Everyday workflow patterns, stacked changelists, and sync conflict resolution.
* **[Direct Materialization Mode (`--no-fuse`)](https://mrtnzjsh.github.io/niebla/workflows/direct-materialization/):** Running Niebla in containers, VMs, and environments without `/dev/fuse`.
* **[Offline Mode & Deferred Sync](https://mrtnzjsh.github.io/niebla/workflows/offline-mode/):** Local caching strategies for working without internet access.
* **[In-Tree Governance (`OWNERS.toml`)](https://mrtnzjsh.github.io/niebla/workflows/in-tree-governance/):** Subtree ownership rules, required sign-offs, and automated CI runner gates.
* **[Observability & Log Aggregation](https://mrtnzjsh.github.io/niebla/recipes/logging-observability/):** Forwarding daemon logs and CloudEvents into Loki, Vector, Promtail, and SIEMs.
* **[Troubleshooting Runbook](https://mrtnzjsh.github.io/niebla/workflows/troubleshooting/):** Unmounting zombie FUSE mounts, socket recovery, and database advisory lock debugging.

### Cotejo Code Review
* **[Review Lifecycle & Diffs](https://mrtnzjsh.github.io/niebla/cotejo/review-workflows/):** Navigating intra-line diffs, inter-patchset comparisons, and draft comments.
* **[AI Code Hints & Personas](https://mrtnzjsh.github.io/niebla/cotejo/ai-assistance/):** 3-tier governance and one-click code suggestion application.
* **[Deployment, Auth & Theming](https://mrtnzjsh.github.io/niebla/cotejo/deployment-auth/):** Reverse proxy authentication, Tailscale integration, and Catppuccin themes.

### Technical & API References
* **[CLI Technical Reference](https://mrtnzjsh.github.io/niebla/reference/cli/):** Comprehensive flag specification, JSON contracts, and automation examples.
* **[Cotejo HTTP & REST API](https://mrtnzjsh.github.io/niebla/reference/cotejo-api/):** Endpoints, HTMX payloads, and CI check run status reporting (`/api/v1/ci/status`).
* **[Security & Authentication Architecture](https://mrtnzjsh.github.io/niebla/reference/security-auth/):** Zero-trust models, gRPC bearer tokens, TLS 1.3, and Tailscale mesh security.
* **[gRPC Service Specification](https://mrtnzjsh.github.io/niebla/reference/grpc-api/):** `NieblaService` Protobuf API, streaming chunk semantics, and error codes.
* **[CloudEvents v1.0 & Webhooks](https://mrtnzjsh.github.io/niebla/reference/events/):** Event schemas and timing-safe HMAC-SHA256 verification code.
* **[Disaster Recovery & ZFS Streaming](https://mrtnzjsh.github.io/niebla/recipes/disaster-recovery/):** Step-by-step standby promotion and envelope-encrypted backup restore.

---

## Background & Academic References

While Niebla is an independent, open-source implementation built on modern cloud-native standards (Go, PostgreSQL, gRPC, Content-Addressable Storage, and NixOS), its workflow philosophy is informed by empirical research on monorepo version control and modern code review:

* **[Why Google Stores Billions of Lines of Code in a Single Repository](https://cacm.acm.org/research/why-google-stores-billions-of-lines-of-code-in-a-single-repository/)** (Communications of the ACM, 2016)  
  *Rachel Potvin and Josh Levenberg* — Details the operational advantages of monolithic repositories, server-authoritative trunk workflows, and virtualized Copy-on-Write workspace mounts over distributed cloning.
* **[Modern Code Review: A Case Study at Google](https://sadowski.github.io/publications/ICSE18-Critique.pdf)** (ACM/IEEE International Conference on Software Engineering, 2018)  
  *Caitlin Sadowski, Emma Söderberg, Luke Church, Michal Sipko, and Alberto Bacchelli* — Studies the mechanics of Critique: linear patchset stacks, attention sets, small review increments, and separating human feedback from automated bot signals.
* **[Software Engineering at Google: Lessons Learned from Programming Over Time](https://abseil.io/resources/swe-book)** (O'Reilly Media, 2020)  
  *Titus Winters, Tom Manshreck, and Hyrum Wright* — Specifically Chapter 16 (*Version Control and Branch Management*) and Chapter 19 (*Critique: Google's Code Review Tool*), analyzing trunk-based development and the breakdown of long-lived pull-request branches.

---

## License

Niebla is open source under the [MIT License](LICENSE).
