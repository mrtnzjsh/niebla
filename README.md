# Niebla: Unified Monorepo & Code Review Platform

[![Documentation](https://img.shields.io/badge/docs-mrtnzjsh.github.io%2Fniebla-blue?style=flat-square)](https://mrtnzjsh.github.io/niebla/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg?style=flat-square)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![NixOS Module](https://img.shields.io/badge/NixOS-Module%20Ready-5277C3?style=flat-square&logo=nixos)](https://nixos.org)

**Niebla** is an open-source, server-authoritative monorepo version control and code review platform designed for high-throughput distributed engineering and autonomous AI agent workflows.

---

## The Challenge: Git in Distributed and Agentic Workflows

Git was designed for decentralized, loosely-coupled repositories where every contributor maintains a full clone of project history. For independent libraries and distributed open-source collaboration, this architecture is battle-tested. 

However, modern engineering teams and autonomous AI coding systems face structural friction when applying a decentralized VCS model to high-throughput monorepos:

### 1. The Cost of Client-Side History in Distributed Monorepos
* **Redundant Packfile Replication:** In a decentralized model, every workstation, ephemeral CI runner, and developer environment must download and store the entire repository DAG. In large monorepos, routine checkouts turn into multi-gigabyte, bandwidth-intensive bottlenecks.
* **Context Switching & Cache Invalidation:** Branch checkouts and stash cycles repeatedly mutate the local working tree, invalidating incremental compiler caches, language server indexes, and IDE file watchers.
* **Cross-Project Coordination:** Atomic cross-cutting changes—such as updating a shared internal API across multiple services—require synchronizing multi-repo pull requests or navigating fragile `git worktree` setups with shared `.git` state.

### 2. The Breaking Point of Autonomous AI Swarms
Modern software development increasingly involves autonomous coding agents (Claude Code, Cursor, Google Antigravity, and headless LLM pipelines) working concurrently alongside humans:
* **High Workspace Concurrency:** Running multiple AI agents in parallel requires dozens of isolated working copies. In Git, provisioning separate clones or worktrees rapidly exhausts disk space and introduces stash collisions.
* **Rebase Sprawl & Merge Vulnerabilities:** LLMs excel at scoped code modifications but frequently hallucinate or introduce silent regressions during complex three-way merge resolutions and interactive rebase squashes.
* **Review Queue Saturation:** High-frequency, machine-generated pull requests overwhelm human review queues without strict queue segregation and structured attention tracking.

### How Niebla Solves It: The Server-Authoritative Monorepo

Niebla re-architects monorepo version control as a **central, high-throughput service**:

1. **Zero-Clone Virtual Workspaces (< 15ms):** Humans and AI agents spin up isolated workspaces instantly. Clean files stream on-demand via FUSE (`nieblafs`) or direct materialization (`--no-fuse`), consuming virtually zero initial disk space (< 100 KB) and deduplicating blobs across a shared local Content-Addressable Storage (CAS) cache.
2. **Instant $O(\text{changed})$ Status:** Local edits route to a private Copy-on-Write (CoW) scratch overlay. Clean files are never scanned on disk; status and diffs compute in sub-millisecond time.
3. **Linear Changelists & Cloud Cascades:** Work is shelved into server-authoritative Changelists (CLs). When a parent change commits to trunk, Niebla's background cascade engine automatically rebases dependent stacked changelists in the cloud.
4. **Attention-Aware Review (Cotejo):** Review code in a blazing-fast web UI featuring Myers intra-line syntax diffs, one-click patch application, and segregated review queues that prevent automated agent output from drowning human attention.
5. **Deterministic Agent Integration:** Native `--json` CLI contracts, granular in-tree `OWNERS.toml` governance, and workstation skill adapters allow agents and humans to safely co-develop on a unified trunk.

---

## See Niebla in Action

From zero to code review in under 30 seconds: creating a virtual workspace, editing code in `vim`, instant diffing, shelving to a new Changelist, reviewing in Cotejo, and landing atomically onto trunk:

![Niebla Developer Workflow](assets/demo.gif)

---

## Why Niebla? (Git vs. Niebla)

| Dimension | Distributed Git + GitHub/GitLab | Niebla + Cotejo |
| :--- | :--- | :--- |
| **Workspace Creation** | **Highly variable**: Seconds for local worktrees, but scales into minutes for fresh clones or large monorepos | **Guaranteed < 15ms ($O(1)$)**: Zero-clone virtual mount or streaming materialization regardless of repo size |
| **Disk Footprint** | **Variable to massive**: Megabytes to tens of gigabytes (scales with historical packfiles & checkout size) | **Guaranteed < 100 KB on creation**: Scales strictly with local modifications via shared CAS deduplication |
| **Status & Diffs** | $O(\text{all repository files})$ | **$O(\text{modified files})$** via private Copy-on-Write (CoW) scratch overlay |
| **Branching Model** | Non-linear branches, stash juggling, merge commits | **Server-authoritative Changelists (CLs)** with linear monotonic trunk commits |
| **Code Review** | Pull requests with branch rebasing and review drift | **Cotejo UI**: Intra-line Myers diffs, inter-patchset comparisons, and attention sets |
| **AI Agent Support** | Scraping CLI output or pushing speculative branches | **Deterministic `--json` contracts**, `OWNERS.toml` gates, and agent harness skills |
| **Conflict Resolution** | Local merge conflicts and rebase squashes | **Cloud Cascade Engine**: Automatically rebases dependent child CLs when parent lands |

---

## Quickstart: Up and Running in 30 Seconds

### The 5-Command Developer Workflow

Once connected to a Niebla server (or running locally), everyday development is frictionless:

```bash
# 1. Spin up an instant, isolated virtual workspace (< 15ms)
niebla workspace create feature-api
cd ~/niebla/workspaces/feature-api

# 2. Make changes with your favorite editor
vim main.go

# 3. Instant status and diffs (scans only changed files)
niebla status
niebla diff

# 4. Shelve changes into an immutable Changelist for review
niebla shelve -m "feat(api): implement healthcheck endpoint"
# -> Shelved CL 6: http://127.0.0.1:8080/cl/6

# 5. Open Cotejo in your browser, review diffs, and submit to trunk
niebla submit --cl 6
```

---

### Deployment & Installation Options

#### Option A: Declarative NixOS Deployment (Recommended)

If you run **NixOS**, Niebla ships with a self-bootstrapping module that automatically compiles binaries, configures PostgreSQL, runs migrations, seeds Depot Root (CL 1), and boots both `niebla-server` and `cotejo`:

```nix
# flake.nix
inputs.niebla.url = "github:mrtnzjsh/niebla";

# configuration.nix
{
  services.niebla.enable = true;
}
```

Run `sudo nixos-rebuild switch` and your monorepo and review server are immediately live.

---

#### Option B: Standalone Manual Installation (Pure Go)

Niebla is written in pure Go and builds with **zero CGO dependencies**:

```bash
# 1. Compile the binaries
cd niebla
CGO_ENABLED=0 go build -o /usr/local/bin/niebla ./cmd/niebla
CGO_ENABLED=0 go build -o /usr/local/bin/niebla-server ./cmd/niebla-server
CGO_ENABLED=0 go build -o /usr/local/bin/nieblafs ./cmd/nieblafs
CGO_ENABLED=0 go build -o /usr/local/bin/cotejo ./cmd/cotejo

# 2. Start PostgreSQL (or use an existing instance)
docker run -d --name niebla-pg -p 5432:5432 -e POSTGRES_PASSWORD=postgres postgres:16-alpine

# 3. Start repository daemon & web review server
niebla-server --db-url "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable" &
cotejo --db-url "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable" &
```

---

## Key Capabilities

### 1. Virtual Workspaces (`niebla workspace`)
* **FUSE Mode (Default):** Virtual mount point backed by `nieblafs`. Full POSIX mutation support (`mkdir`, `touch`, file creation, `rm`, `mv`) routing changes seamlessly to a local Copy-on-Write (CoW) scratch overlay while virtualizing clean files. Storage on creation is virtually zero (< 100 KB).
* **Direct Materialization (`--no-fuse`):** Optimized for macOS (Apple Silicon & Intel APFS), Windows (native `niebla.exe`), Docker containers, Kubernetes pods, CI runners, and environments where `/dev/fuse` is unavailable. Uses parallel streaming and local caching for fast checkouts with automatic backslash and Unicode NFC normalization.
* **Mount Management:** Cleanly attach, detach, or recover workspace mounts with `niebla workspace mount` and `unmount`.

#### Cross-Platform Client Matrix

| Operating System | Supported Mode | Executable | Workspace Creation Command | Client Capabilities & Semantics |
| :--- | :--- | :--- | :--- | :--- |
| **Linux (NixOS, Ubuntu, RHEL)** | FUSE (Default) & `--no-fuse` | `niebla`, `nieblafs` | `niebla workspace create <name>` | Zero-clone FUSE virtual mount backed by local CoW scratch overlay. |
| **macOS (Darwin ARM64 / M-Series & Intel)** | Direct (`--no-fuse`) | `niebla` | `niebla workspace create <name> --no-fuse --server <addr>` | Direct APFS materialization. Decomposed Unicode (NFD) automatically normalized to NFC. |
| **Windows (10, 11, Server x64)** | Direct (`--no-fuse`) | `niebla.exe` | `niebla.exe workspace create <name> --no-fuse --server <addr>` | Standalone Windows binary (cmd/PowerShell). Backslashes normalized; NTFS case collisions safely intercepted. |
| **Windows WSL2** | FUSE & `--no-fuse` | `niebla`, `nieblafs` | `niebla workspace create <name>` | Full Linux capabilities within WSL2 environment. |

### 2. Cotejo: High-Performance Code Review
* **Two-Pass Intra-Line Diff Engine:** Line-level LCS diffs paired with character- and word-level slicing, retaining language syntax highlighting inside diff additions and deletions.
* **Inter-Patchset Diffs:** Compare **Patchset 1 vs. Patchset 2** to review *only* the new changes made in response to feedback.
* **The Attention Set:** Automatically tracks whose turn it is to act (Author vs. Reviewers), preventing reviews from stalling in limbo.
* **One-Click Patch Application:** Reviewers or AI agents can provide replacement code suggestions; authors apply them with a single click (`✓ Apply Suggestion`), synthesizing a new patchset automatically.
* **Vim Keyboard Ergonomics:** Rapid file navigation (`j`/`k`), jump to comments (`n`/`p`), and toggle reviewed files (`m`).

### 3. Monotonic Trunk & Stacked Development
* **Strict Linear History:** Submissions serialize via database advisory locks (`pg_advisory_xact_lock`), incrementing a monotonic revision counter ($r1, r2, r3\dots$).
* **Stacked Changelists:** Branch one feature from another using `niebla workspace create child --cl <parent_id>`.
* **Automated Cloud Cascades:** When a parent CL commits to trunk, Niebla's background cascade engine automatically rebases dependent child changelists in the cloud.

### 4. AI Agent Harness Integration
Niebla ships with built-in skill adapters for modern AI coding environments:
* **Google Antigravity, Claude Code, Cursor, and Universal `.agents`:** Run `niebla skills sync` to link all 4 skills (`niebla`, `niebla-ci`, `niebla-config`, `niebla-review-agent`) across your workstation harnesses.
* **Deterministic Contracts:** Pass `--json` to any command for clean, machine-readable JSON output tailored for autonomous tool execution.

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

## Documentation

Comprehensive documentation, API specs, and runbooks are available at the **[Niebla Documentation Site](https://mrtnzjsh.github.io/niebla/)**:

* **[Architectural Philosophy](https://mrtnzjsh.github.io/niebla/getting-started/philosophy/):** Why server-authoritative Merkle trees outperform distributed packfiles at scale.
* **[Quickstart Walkthrough](https://mrtnzjsh.github.io/niebla/getting-started/quickstart/):** Step-by-step tutorial from workspace creation to trunk submission.
* **[Workspaces & Storage Economics](https://mrtnzjsh.github.io/niebla/workflows/workspaces/):** Five-layer workstation architecture and disk consumption benchmarks.
* **[Direct Materialization Mode (`--no-fuse`)](https://mrtnzjsh.github.io/niebla/workflows/direct-materialization/):** Running Niebla in containers, VMs, and environments without `/dev/fuse`.
* **[In-Tree Governance (`OWNERS.toml`)](https://mrtnzjsh.github.io/niebla/workflows/in-tree-governance/):** Subtree ownership rules, required sign-offs, and automated CI runner gates.
* **[Cotejo Review Lifecycle](https://mrtnzjsh.github.io/niebla/cotejo/review-workflows/):** Navigating intra-line diffs, inter-patchset comparisons, and draft comments.
* **[CLI Technical Reference](https://mrtnzjsh.github.io/niebla/reference/cli/):** Comprehensive flag specification, JSON contracts, and automation examples.
* **[gRPC Service Specification](https://mrtnzjsh.github.io/niebla/reference/grpc-api/):** `NieblaService` Protobuf API, streaming chunk semantics, and error codes.

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

## Contributing & Community

Contributions are welcome! If you're looking for an entry point:
* Check out issues labeled [`good first issue`](https://github.com/mrtnzjsh/niebla/issues?q=is%3Aissue+is%3Aopen+label%3A%22good+first+issue%22) for bite-sized improvements to the CLI, Cotejo UI, or docs.
* Review [`CONTRIBUTING.md`](CONTRIBUTING.md) for local development workflows and test commands.

---

## License

Niebla is open source under the [MIT License](LICENSE).
