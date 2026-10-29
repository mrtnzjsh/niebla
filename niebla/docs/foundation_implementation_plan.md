# Niebla: Foundation & Composable CI Implementation Plan
## Execution Checklist for Components 1, 2, and 3

> **Context:** This plan governs the implementation of the first three foundational components identified in the [Agentic Development Analysis](file:///home/matatan/Developer/niebla/niebla/docs/agentic_development_analysis.md) and [Distributed Architecture Analysis](file:///home/matatan/Developer/niebla/niebla/docs/distributed_architecture_analysis.md).
>
> **Core Engineering Principles:**
> 1. **Zero Backward-Compatibility Debt:** Because Niebla has no production users yet, schema improvements are applied directly to the base migrations (`0001_init.sql`, `0002_critique.sql`) rather than carrying legacy table structures.
> 2. **Composition Over Coupling:** All new subsystems (`pkg/ignore`, `pkg/materialize`, `pkg/ci`) are designed as standalone, interface-driven packages with zero circular or concrete dependencies on `internal/db` or `internal/grpc`.
> 3. **Thorough Verification & Testcontainers:** Every component includes unit tests for pure logic and `testcontainers-go` (`postgres:16-alpine`) + ephemeral HTTP/gRPC test servers for end-to-end integration and concurrency verification.
> 4. **Small, Atomic Commits (No "Super Commits"):**
>    - **One Logical Unit per Commit:** Each sub-step below maps to its own self-contained, atomic commit. Never bundle multiple subsystems, unrelated refactors, or cross-component changes into a single commit.
>    - **Tests Travel With Code:** Every commit that introduces or modifies logic MUST include its corresponding unit or integration tests in that same commit, and `go test` must pass before committing.
>    - **Target Size:** Keep commits focused (~50–250 lines of production code + tests) with conventional commit messages (`feat(cas): ...`, `feat(tree): ...`, `docs(skills): ...`).

---

## Architecture & Composition Overview

```mermaid
flowchart TD
    subgraph Contracts["Pure Domain & Composable Packages (Zero DB/gRPC Coupling)"]
        Tree["pkg/tree<br>(Merkle Hashes, 3-Way Merge, Manifests)"]
        CAS["pkg/cas<br>(Relative Sharded Blob Store)"]
        Ignore["pkg/ignore<br>(.nieblaignore Matcher Interface)"]
        Mat["pkg/materialize<br>(FUSE-less Tree Materializer)"]
        Events["pkg/events<br>(CloudEvents 1.0 Emitter & Sinks)"]
        CI["pkg/ci<br>(Runner Dispatcher, Providers & Auth)"]
    end

    subgraph Adapters["Application Wiring & Transport Layer"]
        DB["internal/db<br>(PostgreSQL Queries & Advisory Locks)"]
        GRPC["internal/grpc<br>(NieblaService Server)"]
        CLI["internal/cli<br>(Human & --json CLI Commands)"]
        Web["internal/web<br>(Cotejo Review & Check Runs HTTP API)"]
    end

    GRPC --> DB
    GRPC --> CAS
    GRPC --> Tree
    GRPC --> Events

    CLI --> Ignore
    CLI --> Mat
    CLI --> Tree

    Events -.->|Decoupled Event Stream| CI
    CI --> Mat
    CI -.->|HTTP/gRPC Status Callback| Web
```

---

## Component 1: Core Storage & Trunk Integrity Overhaul
**References:**
- [Agentic Analysis §2.1: Trunk Head Submission & Linear Revisions](file:///home/matatan/Developer/niebla/niebla/docs/agentic_development_analysis.md#L128-L149)
- [Agentic Analysis §2.3: CAS Binary Tree Manifests](file:///home/matatan/Developer/niebla/niebla/docs/agentic_development_analysis.md#L161-L167)
- [Distributed Analysis Gap 1: Distributed Submit Race Condition](file:///home/matatan/Developer/niebla/niebla/docs/distributed_architecture_analysis.md#L92-L116)
- [Distributed Analysis Gap 2: Host-Local Filesystem Path Storage in CAS](file:///home/matatan/Developer/niebla/niebla/docs/distributed_architecture_analysis.md#L120-L138)

### Commit 1.1: `feat(cas): store portable relative sharded keys instead of host-absolute paths`
- [x] Update [`pkg/cas/cas.go`](file:///home/matatan/Developer/niebla/niebla/pkg/cas/cas.go) (`BlobWriter.Commit` and `Storage.RelativeBlobPath`) so `Commit()` returns the portable relative sharded key (`ab/cd/<sha256>`) for `storagePath`.
- [x] Ensure `Storage.OpenBlob` and `Storage.BlobPath` resolve relative keys against `s.baseDir` cleanly.
- [x] Add unit tests in `pkg/cas/cas_test.go` verifying `Commit()` returns relative paths and blobs remain readable after relocating `baseDir`.

### Commit 1.2: `feat(tree): add deterministic binary manifest serialization`
- [x] Add `MarshalManifest(entries []Entry) ([]byte, error)` and `UnmarshalManifest(data []byte) ([]Entry, error)` in [`pkg/tree/tree.go`](file:///home/matatan/Developer/niebla/niebla/pkg/tree/tree.go).
- [x] Add unit tests in `pkg/tree/tree_test.go` verifying deterministic round-trip serialization on empty, single-entry, and 1,000-entry directory trees.

### Commit 1.3: `feat(db): replace row-per-entry tree_nodes with compact trees manifest table`
- [x] Replace `tree_nodes` in [`internal/db/migrations/0001_init.sql`](file:///home/matatan/Developer/niebla/niebla/internal/db/migrations/0001_init.sql) with `trees (tree_hash BYTEA PRIMARY KEY, manifest BYTEA NOT NULL, entry_count INT NOT NULL, created_at TIMESTAMPTZ DEFAULT now())`.
- [x] Update `InsertTreeNodes`, `GetTreeEntries`, and `GetNodeEntry` in [`internal/db/queries.go`](file:///home/matatan/Developer/niebla/niebla/internal/db/queries.go) to read/write serialized manifests with zero signature changes.
- [x] Verify existing and updated tree queries pass in `internal/db/db_test.go` via `testcontainers-go` (`postgres:16-alpine`).

### Commit 1.4: `feat(db): add sequential trunk_revision and pg_advisory_xact_lock to SubmitCL`
- [x] Update `changelists` in [`internal/db/migrations/0001_init.sql`](file:///home/matatan/Developer/niebla/niebla/internal/db/migrations/0001_init.sql) with `trunk_revision BIGINT UNIQUE`, `trunk_revision_seq`, and seed CL 1 with `trunk_revision = 1`.
- [x] Update `GetHeadCL` in [`internal/db/queries.go`](file:///home/matatan/Developer/niebla/niebla/internal/db/queries.go#L196-L225) to order by `ORDER BY trunk_revision DESC LIMIT 1`.
- [x] Guard `SubmitCL` in [`internal/db/queries.go`](file:///home/matatan/Developer/niebla/niebla/internal/db/queries.go#L278) with `SELECT pg_advisory_xact_lock(0x4e494542)` and accept an optional `TrunkMergeFunc` callback to compute the merged root tree hash before assigning `nextval('trunk_revision_seq')`.
- [x] Add `testcontainers-go` tests in `internal/db/db_test.go` verifying out-of-order CL ID submission (`CL 3` submitted before `CL 2`) assigns `trunk_revision` 2 and 3 in strict commit order.

### Commit 1.5: `feat(grpc): perform 3-way Merkle tree merge onto trunk head during SubmitCL`
- [x] Wire `tree.ThreeWayTreeMerge` into `Server.SubmitCL` in [`internal/grpc/server.go`](file:///home/matatan/Developer/niebla/niebla/internal/grpc/server.go#L296-L356) so submitting a CL whose `parent_cl` lags behind `HeadCL` automatically merges disjoint subtrees onto trunk head, or aborts with `codes.FailedPrecondition` (`needs_rebase = true`) on file conflict.
- [x] Add `internal/grpc/submit_concurrency_test.go` (using `testcontainers-go`):
  - **10-Goroutine Concurrent Submit Race Test:** 10 concurrent goroutines submitting disjoint CLs branched from CL 1 -> all 10 succeed as `r2..r11` with all 10 files present in the final head tree.
  - **Conflicting Concurrent Submit Test:** 2 goroutines editing the same file -> 1 succeeds, 1 fails with `FailedPrecondition` and marks `needs_rebase = true`.

### Commit 1.6: `docs(skills): update niebla skill with trunk_revision and atomic 3-way submit semantics`
- [x] Update [`~/.gemini/config/skills/niebla/SKILL.md`](file:///home/matatan/.gemini/config/skills/niebla/SKILL.md) to document linear `trunk_revision` ($r1, r2, \dots$) vs. `cl_id` and automatic 3-way trunk merges during `niebla submit`.

---

## Component 2: FUSE-less Materialization (`pkg/materialize`), `.nieblaignore` (`pkg/ignore`), & `--json` CLI
**References:**
- [Agentic Analysis §1.1: FUSE-less Direct Materialization](file:///home/matatan/Developer/niebla/niebla/docs/agentic_development_analysis.md#L89-L95)
- [Agentic Analysis §1.3: Scratch Scanning & `.nieblaignore` Parity](file:///home/matatan/Developer/niebla/niebla/docs/agentic_development_analysis.md#L107-L114)
- [Agentic Analysis §1.4: Dual-Mode CLI Contracts (`--json`)](file:///home/matatan/Developer/niebla/niebla/docs/agentic_development_analysis.md#L117-L123)
- [Distributed Analysis §3.1: Stateless Materialization Contract (`niebla export`)](file:///home/matatan/Developer/niebla/niebla/docs/distributed_architecture_analysis.md#L239-L240)

### Commit 2.1: `feat(ignore): add standalone composable .nieblaignore matcher package`
- [x] Create `pkg/ignore` (`Matcher` interface) with zero internal dependencies, supporting comments (`#`), directory rules (`build/`), globs (`*.o`, `node_modules`), anchored paths (`/dist`), negation (`!important.log`), and built-in exclusion of `.niebla/`.
- [x] Add comprehensive table-driven unit tests in `pkg/ignore/ignore_test.go`.

### Commit 2.2: `feat(cli): integrate .nieblaignore filtering into ScanScratchChanges`
- [x] Wire `ignore.Matcher` into [`ScanScratchChanges`](file:///home/matatan/Developer/niebla/niebla/internal/cli/status.go#L92) so ignored directories return `filepath.SkipDir` immediately without making gRPC `ResolvePath` calls or uploading build artifacts during `shelve`.
- [x] Add unit/integration tests in `internal/cli/status_test.go` verifying ignored directories and files are excluded from status and shelve scans.

### Commit 2.3: `feat(materialize): add standalone concurrent tree materializer with CAS hardlink fast-path`
- [x] Create standalone package `pkg/materialize` (`MaterializeTree` accepting `tree.FetchTreeFunc`, `BlobFetcher`, and `Options{Concurrency, HardlinkCAS, Filter}`).
- [x] Add unit tests in `pkg/materialize/materialize_test.go` testing both hardlink mode (inode sharing from local CAS cache) and standard writable file copy mode with POSIX permissions (`0644` / `0755`).

### Commit 2.4: `feat(cli): add 'niebla export' and 'workspace create --no-fuse'`
- [x] Add `niebla export --cl <id> [--patchset <n>] --dest <dir>` in `internal/cli/export.go` backed by `pkg/materialize`.
- [x] Add `--no-fuse` flag to `niebla workspace create` in `internal/cli/workspace.go` for FUSE-less container/macOS checkouts.
- [x] Add `testcontainers-go` integration test in `internal/cli/export_integration_test.go` verifying end-to-end workspace creation, `.nieblaignore` filtering on shelve, and `niebla export` byte-for-byte verification.

### Commit 2.5: `feat(cli): add global --json flag and machine-readable output contracts`
- [x] Add global `--json` flag to `RootFlags` in `internal/cli/root.go` and structured JSON output envelopes for `status`, `shelve`, `submit`, `sync`, and `export`.
- [x] Add CLI JSON schema output tests in `internal/cli/json_output_test.go`.

### Commit 2.6: `docs(skills): update niebla skill with --no-fuse, export, .nieblaignore, and --json`
- [x] Update [`~/.gemini/config/skills/niebla/SKILL.md`](file:///home/matatan/.gemini/config/skills/niebla/SKILL.md) to document `workspace create --no-fuse`, `niebla export`, `.nieblaignore`, and `--json` usage for AI agents.

---

## Component 3: Unified Server Event Bus & Composable `niebla-ci` Orchestrator
**References:**
- [Distributed Analysis Gap 4: Missing Event Emission in gRPC Server](file:///home/matatan/Developer/niebla/niebla/docs/distributed_architecture_analysis.md#L159-L170)
- [Distributed Analysis §3: Composable CI Runner Orchestration (`niebla-ci`)](file:///home/matatan/Developer/niebla/niebla/docs/distributed_architecture_analysis.md#L188-L337)

### Commit 3.1: `feat(grpc): wire CloudEvents emitter into niebla-server`
- [x] Inject `pkgevents.Emitter` into [`internal/grpc/server.go`](file:///home/matatan/Developer/niebla/niebla/internal/grpc/server.go) (`Server.SetEmitter`) and [`cmd/niebla-server/main.go`](file:///home/matatan/Developer/niebla/niebla/cmd/niebla-server/main.go).
- [x] Emit standardized CloudEvents on `ShelveCL` (`niebla.cl.created`), `SubmitCL` (`niebla.cl.submitted`), and `CascadeStackRebase` (`niebla.cl.rebased` / `niebla.cl.rebase_conflict`).
- [x] Add unit/integration tests in `internal/grpc/events_test.go` asserting events are emitted on gRPC calls.

### Commit 3.2: `feat(db,web): add cl_check_runs table, Check Runs API, and submit gate`
- [x] Add `cl_check_runs` table to [`internal/db/migrations/0002_critique.sql`](file:///home/matatan/Developer/niebla/niebla/internal/db/migrations/0002_critique.sql) and DB queries (`UpsertCheckRun`, `ListCheckRunsForPatchset`, `AreCheckRunsGreen`).
- [x] Expose provider-agnostic check status endpoints (`ReportCheckRun` gRPC RPC and `POST /api/v1/ci/status` in Cotejo) and optional `require_green_to_submit` guard in `SubmitCL`.
- [x] Add `testcontainers-go` DB and handler tests for check run state transitions (`queued` -> `running` -> `passed`/`failed`) and submit gating.


### Commit 3.3: `feat(ci): add [ci] config schema, Provider interfaces, and Auth RoundTripper`
- [x] Extend [`internal/config/config.go`](file:///home/matatan/Developer/niebla/niebla/internal/config/config.go) and validation rules with `CIConfig`, `CIRunnerConfig`, and `CIAuthConfig`.
- [x] Create `pkg/ci` core interfaces (`Provider`, `StatusReporter`, `Dispatcher`) and `pkg/ci/auth` composable `http.RoundTripper` (`bearer`, `token`, `basic`, `header`, `hmac`).
- [x] Add unit tests in `pkg/ci/auth/auth_test.go` and `internal/config/config_test.go`.


### Commit 3.4: `feat(ci): implement default local sandbox runner provider`
- [x] Implement `pkg/ci/providers/local` using `pkg/materialize` (`HardlinkCAS`) to materialize patchsets into `/tmp/niebla-ci/cl-<id>-ps-<n>/`, execute the configured command with timeout, capture logs to CAS, and report status via `StatusReporter`.
- [x] Add unit tests in `pkg/ci/providers/local/local_test.go`.


### Commit 3.5: `feat(ci): implement forgejo, github, and webhook remote runner providers`
- [x] Implement `pkg/ci/providers/forgejo` (workflow dispatch API), `pkg/ci/providers/github` (`repository_dispatch` / `workflow_dispatch` API), and `pkg/ci/providers/webhook` (CloudEvents + auth).
- [x] Add `httptest.NewServer` test suites in `pkg/ci/providers/` verifying headers, auth tokens, HMAC signatures, request bodies, and retry behavior.


### Commit 3.6: `feat(cli): add 'niebla ci exec' and 'niebla ci report' commands`
- [x] Add `niebla ci exec` and `niebla ci report` in `internal/cli/ci.go` so remote runners can wrap any build command and automatically report `running` -> `passed`/`failed` + log blobs back to the server.
- [x] Add end-to-end integration test (`pkg/ci/ci_integration_test.go` with `testcontainers-go`) testing the full lifecycle from `niebla shelve` -> event dispatch -> runner execution -> check run status -> `niebla submit`.


### Commit 3.7: `docs(skills): create niebla-ci and niebla-config skills and update niebla skill`
- [ ] Create [`~/.gemini/config/skills/niebla-ci/SKILL.md`](file:///home/matatan/.gemini/config/skills/niebla-ci/SKILL.md) covering `[ci.runners.<name>]`, auth configuration, GitHub/Forgejo workflow authoring with `niebla export` + `niebla ci exec`, and check run troubleshooting.
- [ ] Create [`~/.gemini/config/skills/niebla-config/SKILL.md`](file:///home/matatan/.gemini/config/skills/niebla-config/SKILL.md) documenting `~/.config/niebla/config.toml` (`[events]`, `[ci]`, `[resilience]`) and `niebla config validate`.
- [ ] Update [`~/.gemini/config/skills/niebla/SKILL.md`](file:///home/matatan/.gemini/config/skills/niebla/SKILL.md) to cross-reference `niebla ci` commands and the `require_green_to_submit` guard.

