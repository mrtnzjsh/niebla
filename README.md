# Niebla: Unified Monorepo & Code Review Toolchain

Niebla is a server-authoritative, cloud-native monorepo version control and code review toolchain inspired by Google's **Piper**, **Clients-in-the-Cloud (CitC)**, and **Critique** systems.

**Strictly No Git**: Niebla replaces distributed Git packfiles with virtual, read-only FUSE mounts (`nieblafs`), local Copy-on-Write (CoW) scratch overlays, Content-Addressable Storage (CAS), and a dedicated web-based code review interface (**Cotejo**).

```
┌────────────────────────────────────────────────────────────────────────┐
│                        Developer Workstation                          │
│                                                                        │
│   Virtual Workspace Mount (/niebla/workspaces/<name>)                  │
│   ├── FUSE Inode Cache (Virtual 500k-file tree: < 1MB RAM)            │
│   └── Scratch Overlay (Local CoW modifications: bytes on disk)         │
│                                                                        │
│   CLI Toolchain: niebla status | niebla diff | niebla shelve           │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ gRPC (HTTP/2)
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                     Central Repository Cluster                         │
│                                                                        │
│   niebla-server (:50051)                                               │
│   ├── Merkle Tree Serialization & 3-Way Auto-Rebase Engine            │
│   ├── Central CAS Storage (Deduplicated Content Blobs)                │
│   └── PostgreSQL (Metadata, Patchsets, Reviews, Discussions)          │
│                                                                        │
│   Cotejo Code Review Service (:8080)                                  │
│   ├── Real-Time Critique Web UI (Templ SSR + Chroma Syntax Highlighting)│
│   ├── Attention Set State Machine & Segregated Inboxes                 │
│   ├── Intra-Line Word Diffing & Patchset Inter-Diffs                  │
│   └── Autonomous AI Review Agents & One-Click Patch Application        │
└────────────────────────────────────────────────────────────────────────┘
```

---

## Cotejo: Google Critique-Inspired Code Review

**Cotejo** is Niebla's native, web-based code review interface. Operating on port `8080` (with a live-reload proxy on port `7331` for local development), Cotejo is designed to eliminate the merge skew, review stalls, and noisy notification loops common to Git pull request workflows.

### 1. Patchset Stacks vs. Git Pull Requests

Instead of managing branching, rebasing, and squash merges on developer machines, Cotejo treats reviews as a linear stack of immutable, content-addressed **Patchsets** ($P_1, P_2, \dots$):

* **Immutable Revisions:** Every `niebla shelve` snapshots your workspace's local changes into a new patchset Merkle tree.
* **Inter-Diff Comparisons:** Reviewers can compare any two patchsets directly (e.g. **Patchset 1 vs. Patchset 2**) to see *only* the new lines modified to address review comments, without re-reviewing untouched files.
* **Atomic Linear Submissions:** Submitting a changelist (`niebla submit`) performs a server-side three-way Merkle merge onto trunk, guaranteeing a clean, linear repository history.

### 2. The Attention Set: Eliminating Review Deadlocks

In typical code review systems, pull requests stall because it is ambiguous who needs to act next. Cotejo implements the **Attention Set state machine**:

* **Author Shelves CL:** Reviewers enter the Attention Set; the author is cleared.
* **Reviewer Requests Changes:** Reviewer leaves the Attention Set; author enters.
* **Author Pushes Update:** Attention flips back to reviewers.
* **Ready to Submit:** Once required approvals are granted, attention returns to the author to trigger `niebla submit`.

At any time, developers can check **My Review Queue** (`/cls?tab=my-reviews`) to see only changelists requiring their immediate attention.

### 3. High-Fidelity Intra-Line Word Diffing

Cotejo's diff engine executes a **two-pass comparison**:
1. **Line-Level LCS:** Identifies added, deleted, and unchanged lines.
2. **Intra-Line Token Slicing:** For modified lines, a second pass highlights the exact modified words or characters.
3. **Chroma Syntax Coloring:** Code retains full language syntax highlighting (keywords, types, strings) inside diff additions and deletions.

### 4. Review Workflows & Interactivity

* **Multi-Draft Commenting:** Author comments across dozens of files in an unsubmitted draft state (`is_draft = true`). Publish all notes in a single batch with **"Publish Comments"** to prevent spamming notifications.
* **Multi-Line Drag Anchoring:** Click and drag across line numbers ($L_{\text{start}} \dots L_{\text{end}}$) to anchor comments to complete functions or multi-line blocks.
* **Thread Resolution Gating:** Discussions require explicit resolution (`Resolved`). Repositories can block submissions via `allow_unresolved_comments = false` until all feedback is addressed.
* **Per-Reviewer Progress Tracking:** Every file tracks review state independently (`reviewed` vs. `not reviewed` per user). Opening a file marks it reviewed; a live header progress bar displays `X of Y files reviewed by <user>`.
* **Vim Keyboard Shortcuts:** Navigate files (`j`/`k`), jump between comments (`n`/`p`), and toggle review status (`m`) without touching the mouse.

### 5. Autonomous AI Review Agents & One-Click Patches

Cotejo integrates deeply with AI review agents and static analysis bots via a **3-Tier Governance Hierarchy**:

* **Tier 1 (Host Baseline — `~/.config/niebla/*.toml`):** Configures LLM endpoints (OpenAI API, vLLM, Ollama, LiteLLM) and token limits.
* **Tier 2 (Shared Repo Catalog — `.niebla/agents.toml`):** Defines reusable review agents (`type = "ai"`, `type = "cas_binary"`, `type = "in_tree"`).
* **Tier 3 (Subtree Policy — `OWNERS.toml`):** Subtree maintainers attach specific agents (e.g. `agents = ["a11y-bot", "sec-bot"]`) to their directories.

**One-Click Patch Application:** When an AI agent or human reviewer suggests replacement code, authors can click **`✓ Apply Suggestion`**. Cotejo splices the fix into the file, streams the new blob to CAS, computes a new Merkle tree, generates **Patchset $N+1$**, and automatically marks the comment resolved.

### 6. Subtree Governance (`OWNERS.toml`)

Code ownership is defined hierarchically within the repository tree:

```toml
# services/greeter/OWNERS.toml
approvers = ["alice", "bob"]
reviewers = ["carol"]
required_approvals = 1
require_green_ci = true
allow_unresolved_comments = false

[per_file]
"*.go" = ["alice"]
```

`niebla submit` evaluates touched files against all parent `OWNERS.toml` files, rejecting submissions unless designated approvers sign off and CI check runs pass.

### 7. Deployment, Identity & Theming

* **Reverse Proxy Authentication:** Native support for Tailscale Auth, Cloudflare Access, Traefik, and NGINX using trusted header forwarding (`mode = "headers"` with `trusted_proxies`).
* **Real-Time Server-Sent Events (SSE):** Live UI reactivity at `/events` pushes comment publications, CI updates, and patchset uploads without WebSocket complexity.
* **Catppuccin & Custom Theming:** Presets include `catppuccin-mocha`, `catppuccin-latte`, `catppuccin-frappe`, `tokyo-night`, `dracula`, `nord`, and custom color token overrides.

---

## Documentation Site

Explore the full documentation site built with Astro & Starlight:

* **[Getting Started: Quickstart & Hello World Walkthrough](http://localhost:4321/niebla/getting-started/quickstart/)**
* **[Workspaces & Storage Economics](http://localhost:4321/niebla/workflows/workspaces/)**
* **[Cotejo Overview & Philosophy](http://localhost:4321/niebla/cotejo/overview/)**
* **[The Code Review Lifecycle](http://localhost:4321/niebla/cotejo/review-workflows/)**
* **[AI Code Hints & Review Personas](http://localhost:4321/niebla/cotejo/ai-assistance/)**
* **[Deployment, Auth & Theming](http://localhost:4321/niebla/cotejo/deployment-auth/)**
