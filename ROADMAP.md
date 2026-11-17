# Niebla Architecture & Product Roadmap

This document outlines the strategic engineering roadmap for **Niebla**, covering near-term milestones (v0.2.x), mid-term enterprise capabilities (v0.3.x–v0.5.x), and long-term targets toward v1.0.

---

## 1. Security, Cryptography & Compliance

Niebla adheres to a defense-in-depth model that decouples high-performance local compilation from cryptographic boundary protections. The following roadmap items govern upcoming security hardening:

### Explicit Server-Side TLS 1.3 Enforcement
* [ ] **Strict TLS 1.3 Server Listeners:** While Niebla HTTP and gRPC clients strictly enforce `MinVersion: tls.VersionTLS13`, enhance `cmd/niebla-server/main.go` to construct an explicit `*tls.Config{MinVersion: tls.VersionTLS13}` for gRPC and Cotejo listeners to reject TLS 1.2 handshakes by default.
* [ ] **Automated Cipher Suite Auditing:** Add continuous integration regression tests asserting that legacy CBC ciphers, insecure renegotiation, and non-AEAD suites fail immediately during handshake negotiation.

### Cloud KMS & Hardware Security Module (HSM) Integration
* [ ] **Enterprise Key Encryption Key (KEK) Providers:** Extend `niebla/pkg/crypto/keys.go` beyond `env`, `file`, and `passphrase` sources to support cloud Key Management Services (AWS KMS, Google Cloud KMS, Azure Key Vault, and HashiCorp Vault Transit Engine).
* [ ] **Envelope KEK Delegation:** Allow operators to seal and unwrap the ephemeral Data Encryption Key (DEK) inside hardware security modules, ensuring master keys never reside unencrypted in server memory or on persistent disk.

### Client-Side Encrypted Remote S3 CAS Storage
* [ ] **Zero-Knowledge Multi-Tenant Blob Caching:** Introduce optional client-side chunk encryption for deployments replicating CAS blobs to shared or multi-tenant object storage (AWS S3, MinIO, Cloudflare R2) outside a private ZFS pool.
* [ ] **Deduplication-Preserving Deterministic Chunking:** Evaluate convergent encryption or per-subtree key rotation profiles to balance CAS deduplication efficiency with untrusted cloud storage protection.

### Cryptographic Audit & Compliance Verification
* [ ] **Zero-Telemetry Formal Guarantee:** Maintain strict zero-telemetry and offline operational guarantees; ensure no external phone-home requests, anonymous analytics, or licensing pings can be introduced into the core toolchain.
* [ ] **SOC 2 Type II / ISO 27001 Mapping Guide:** Provide formalized documentation detailing how Niebla's combination of TLS 1.3 transit encryption, Argon2id/AES-256-GCM backup archives, and OS-level Full Disk Encryption (LUKS, FileVault, BitLocker) fulfills standard enterprise compliance controls.

---

## 2. Core VCS & Virtual Filesystems

* [x] **Sub-Second Virtual Workspaces:** FUSE-backed on-demand streaming with Copy-on-Write (CoW) scratch isolation (`nieblafs`).
* [x] **Direct Materialization (`--no-fuse`):** Zero-copy hardlinking (`os.Link`) and read-only cache protection for non-FUSE and CI environments.
* [x] **Cross-Platform Interoperability:** Native Unicode NFC normalization (`golang.org/x/text/unicode/norm`), APFS/NTFS case-collision detection, and Windows path canonicalization.
* [ ] **Sparse Workspaces & Partial Checkouts:** Dynamic path-filter specification in `niebla workspace create --sparse <patterns...>`, allowing massive multi-gigabyte subtrees to remain unmaterialized until explicitly accessed.
* [ ] **Windows ProjFS Native Filesystem Driver:** Explore native Windows Projected File System (ProjFS) integration as a high-performance alternative to direct materialization on Windows 11.
* [ ] **Fast Forward Trunk Rebase Optimization:** Further optimize large tree 3-way merge resolution (`pkg/tree/diff3.go`) by parallelizing directory sub-tree hashing during cascade rebases.

---

## 3. Code Review & Cotejo Web Platform

* [x] **High-Throughput Web Interface:** Real-time side-by-side and unified diffing with syntax highlighting and intra-line word-level change detection.
* [x] **Attention Set State Machine:** Explicit reviewer/author turn tracking, eliminating notification noise and review stagnation.
* [x] **Single-Click AI Suggestions:** Inline patch application directly from AI review comments without leaving the browser.
* [ ] **Multi-Head Visual Revision Graph:** Interactive web visualization of linear patchset stacks and dependent Changelist trees (`niebla stack`).
* [ ] **Live Collaborative Review Sessions:** WebSockets-backed collaborative cursor and comment drafting across active reviewers.
* [ ] **Dark / Light Theme System Integration:** Native system color scheme synchronization and customizable editor themes in Cotejo.

---

## 4. CI/CD Orchestration & Monorepo Governance

* [x] **Decoupled Check Runs API:** Provider-agnostic status reporting (`POST /api/v1/ci/status`) over CloudEvents.
* [x] **Subtree Governance (`OWNERS.toml`):** Granular approval policies, review bot assignments, and subtree-specific green CI requirements (`require_green_ci = true`).
* [x] **Automated Cross-OS RC Pipeline:** Automated Linux and macOS Apple Silicon test dispatching via Forgejo Actions with non-blocking offline fallbacks.
* [ ] **CAS-Aware Distributed CI Caching:** Shared remote build cache protocol allowing CI runners to reuse intermediate compilation targets across patchsets.
* [ ] **GitHub PR Bidirectional Gateway:** Automated synchronization agent that mirrors public GitHub Pull Requests into internal Cotejo Changelists and updates GitHub status checks.

---

## 5. Enterprise Disaster Recovery & Resilience

* [x] **Encrypted Backup Bundles:** Portable `.nbk` archives combining consistent PostgreSQL dumps and incremental CAS blobs with AES-256-GCM / ChaCha20-Poly1305.
* [x] **ZFS Native Snapshot Replication:** Raw encrypted dataset streaming (`zfs send -w`) for sub-minute disaster recovery failover.
* [ ] **Automated Standby Failover Orchestrator:** Health check daemon to automate DNS/IP failover to a hot standby `niebla-server` instance upon primary node failure.
* [ ] **Continuous Point-in-Time Recovery (PITR):** Streaming PostgreSQL Write-Ahead Log (WAL) archiving to S3 alongside incremental CAS blob snapshots.
