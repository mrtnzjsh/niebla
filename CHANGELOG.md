# Changelog

All notable changes to Niebla are documented in this file, adhering to [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-11-15

### Added
- **Core Virtual Workspace Engine**:
  - Sub-15ms thin workspace provisioning backed by Content-Addressable Storage (CAS).
  - High-throughput FUSE driver (`nieblafs`) with LRU metadata and block caching for Linux systems.
  - Zero-FUSE Direct Materialization Mode (`niebla export` and `--no-fuse`) for containerized agents, CI runners, and macOS human environments.
  - Explicit workspace lifecycle management commands (`niebla workspace create`, `list`, `delete`, `mount`, `unmount`).

- **Server-Authoritative Repository Daemon (`niebla-server`)**:
  - High-performance gRPC server managing Merkle tree manifests, changelists, and patchsets.
  - PostgreSQL metadata layer tracking linear trunk revisions, review status, and branch parentage.
  - Content-Addressable Storage (CAS) vault with two-level hex sharding (`ab/cd/...`) and S3/MinIO driver compatibility.
  - Atomic trunk submit queue with advisory locking and three-way tree merge conflict resolution.
  - CloudEvents webhook emitter and audit logging pipeline for submission, shelve, and check run events.

- **Copy-on-Write (CoW) Scratch Layer**:
  - Instant change detection tracking file mutations exclusively in `/scratch/workspaces/<name>/`.
  - Sub-millisecond `niebla status` and `niebla diff` calculations running in $O(\text{changed})$ time without scanning unmodified tree blobs.
  - Built-in `.nieblaignore` matching engine ignoring build artifacts, IDE directories, and temporary caches.

- **Cotejo Web Code Review Platform (`cotejo`)**:
  - Integrated code review web server built with Go `templ`, Tailwind CSS, and HTMX.
  - Split and unified intra-word diff viewer with Chroma syntax highlighting and customizable themes (including Catppuccin Mocha).
  - Attention set state machines and segregated inbox queues (`My Reviews`, `Out for Review`, `Agent / Automated`, `Submitted / Trunk`).
  - Single-line and multi-line review comments with inline reply modals and resolved/unresolved state tracking.
  - One-click application of AI-suggested code diff blocks directly into the target changelist.

- **AI Code Review Personas & Comment Hints**:
  - Declarative persona registry (`.niebla/agents.toml`) with path-based auto-assignment and automated review voting (`lgtm` / `changes_requested`).
  - Interactive AI hint generation endpoint (`POST /cl/{id}/ai-hint/generate`) featuring Adaptive Context Apertures (Tier 1: lines, Tier 2: block, Tier 3: file).
  - Version-controlled prompt engineering templates under `.niebla/prompts/` (`security.md`, `accessibility.md`, `comment_hint.md`).

- **In-Tree Subtree CI & Check Runs**:
  - Decoupled CI orchestration runner (`niebla ci exec`) supporting `local`, `forgejo`, `github`, and `webhook` providers.
  - Database-backed `cl_check_runs` schema reporting automated build, test, and lint verdicts back to Cotejo.
  - Scoped bearer token authentication for CI execution and tree materialization.

- **Governance & Policy Engine (`OWNERS.toml`)**:
  - Hierarchical scoped approval rules with `set_noparent` inheritance overrides and `@group` expansion.
  - Pattern-based file matching (`[per_file]`) enforcing domain-specific reviews before trunk integration.
  - Presubmit hooks (`.niebla/hooks.toml`) supporting client-side and server-side validation with `--no-verify` and `--force` bypasses.

- **Documentation & Operational Guides**:
  - Production Astro + Starlight documentation site published to GitHub Pages.
  - Comprehensive reference specifications: Cotejo REST API, gRPC API, Events, Configuration, and Security Architecture.
  - Operational recipes: Cloud deployments, Tailscale private mesh networking, ZFS snapshots with 10GbE streaming, and Observability with Vector / Promtail / Loki.

- **Nix & System Integration**:
  - Pure Nix flake packaging for all 4 Go binaries (`niebla`, `niebla-server`, `nieblafs`, `cotejo`) and unified `niebla-suite`.
  - Production-ready NixOS module (`services.niebla`) with automated PostgreSQL database setup, systemd unit generation, and auto-migrations.

