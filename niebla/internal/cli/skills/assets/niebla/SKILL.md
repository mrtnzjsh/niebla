---
name: niebla
description: >-
  Workflows, command syntax, and best practices for interacting with the Niebla
  version control system and Cotejo code review UI. Use when managing virtual workspaces, inspecting CoW scratch changes,
  diffing against base CL blobs, shelving changelists, submitting to trunk, and conducting code reviews with single-line or multi-line code block comments.
---

# Niebla Version Control System Runbook

Niebla is a server-authoritative, cloud-native monorepo VCS (strictly no Git). Workspaces are virtual, read-only FUSE mounts backed by a Copy-on-Write (CoW) scratch overlay and a Content-Addressable Storage (CAS) backend.

---

## 1. Core Principles for AI Agents

1. **Strictly No Git:** Do NOT use `git checkout`, `git add`, `git commit`, `git push`, or `git status`. All version control operations MUST be performed via `niebla`.
2. **Virtual Workspace Mounts & `--no-fuse` Mode:** Workspaces live in `/niebla/workspaces/<name>` (or custom `--mount-dir`). Multiple workspaces run side-by-side concurrently without switching branches in-place. In unprivileged containers, Windows workstations (`niebla.exe`), macOS laptops, or environments without `/dev/fuse`, pass `--no-fuse` to materialize the workspace tree directly to disk. Windows path backslashes and APFS decomposed Unicode (NFD) are automatically normalized to canonical forward slashes and Unicode NFC.
3. **Copy-on-Write (CoW) Scratch & `.nieblaignore`:** Edits made inside `/niebla/workspaces/<name>` are stored in the local scratch overlay (`/scratch/niebla/workspaces/<name>`). Place a `.nieblaignore` file in the workspace root to exclude build artifacts (`node_modules/`, `build/`, `*.o`, with `!negation` support) from `status`, `diff`, and `shelve`.
4. **Machine-Readable `--json` Contracts:** AI agents should pass `--json` to `status`, `shelve`, `submit`, `sync`, `export`, and `workspace` commands for deterministic JSON output.
5. **Automatic Workspace Detection:** Running `niebla` commands inside a workspace directory automatically detects the active workspace via `$PWD`. When running from outside, specify `--workspace <name>`.
6. **Code Review with Cotejo:** Code reviews and changelist inspections are conducted in Cotejo (`http://127.0.0.1:7331` or configured port), featuring side-by-side diffs, patchset history, and multi-line code block comments.
7. **Transport Security & Bearer Authentication:** Single-system development connects to `127.0.0.1:50051` via plaintext loopback without auth by default. For distributed or remote environments, enable TLS (`[server.tls]`, `[core.tls]`) and pass bearer authentication tokens via `NIEBLA_TOKEN=<token>` or `core.auth_token` in `config.toml`.
8. **OWNERS Approval Governance:** Monorepo paths and critical file patterns enforce granular ownership via `OWNERS.toml` files. Changelists affecting owned paths require approval (`lgtm`/`approved`) from designated directory or pattern owners before `niebla submit` will succeed.
9. **Presubmit Hook Gating:** Monorepos declare client and server presubmit hooks in `.niebla/hooks.toml` (Conventional Commits, secret patterns, max file sizes). Client hooks can be bypassed with `--no-verify`; server gates are evaluated under transaction locks and bypassable only via `--force`.
10. **Review Agent Personas:** Specialized review bots (`.niebla/agents.toml`) are auto-assigned upon `niebla shelve`, executing in-tree scripts or precompiled CAS binaries, uploading build logs to CAS, posting compiler/linter errors as inline diff comments, and casting review approvals (`lgtm`/`changes_requested`).

---

## 2. Command Reference

### gRPC Transport Security & Bearer Authentication

Niebla supports optional TLS 1.3 transport encryption and Bearer token authentication across all CLI and daemon operations:

* **Authentication Token Precedence:**
  1. Explicit `--token <token>` flag (or CLI option)
  2. `NIEBLA_TOKEN` environment variable
  3. `core.auth_token` in `~/.config/niebla/config.toml`

* **Configuration in `config.toml`:**
  ```toml
  [server]
  auth_tokens = ["agent-worker-token-xyz", "human-admin-token-abc"]

  [server.tls]
  enabled = true
  cert_file = "/etc/niebla/tls/server.crt"
  key_file = "/etc/niebla/tls/server.key"

  [core]
  auth_token = "agent-worker-token-xyz"

  [core.tls]
  enabled = true
  ca_file = "/etc/niebla/tls/ca.crt"       # optional if using private CA
  server_name = "niebla.internal"          # SNI hostname verification
  ```

* **Server Daemon Security Flags:**
  ```bash
  niebla-server --tls --tls-cert /path/to/server.crt --tls-key /path/to/server.key --token <secret-token>
  ```
  When tokens are configured, the server enforces Bearer token authentication via `Authorization: Bearer <token>` and `X-Niebla-Token` metadata interceptors across all unary and streaming RPCs.

### Workspace Lifecycle & Stateless Export

* **Create a workspace (FUSE or FUSE-less):**
  ```bash
  niebla workspace create <name> [--cl <base_cl>] [--mount-dir <path>] [--no-fuse] [--json]
  ```
  Creates a workspace on the server (bound to head CL by default or `--cl <id>`). By default, mounts the `nieblafs` virtual filesystem at `/niebla/workspaces/<name>` and starts a background Unix control socket (`nieblafs.sock`). The CLI detects daemon readiness deterministically via control socket `ping` in <5ms. When `--no-fuse` is set, materializes the base CL Merkle tree directly into the mount directory without requiring `/dev/fuse`.

* **Stateless tree materialization (`niebla export`):**
  ```bash
  niebla export [--cl <id>] [--patchset <n>] --dest <dir> [--cas-dir <path>] [--concurrency <n>] [--json]
  ```
  Materializes any changelist (or trunk head when `--cl` is omitted) directly into `--dest` using a concurrent worker pool (`pkg/materialize`). When `--cas-dir` points to a local CAS directory on the same filesystem, blobs are zero-copy hardlinked (`os.Link`) in milliseconds for ephemeral CI runners.

* **Direct Content-Addressable Storage (CAS) Ingestion (`niebla cas`):**
  ```bash
  niebla cas put <file> [--json]
  niebla cas get <sha256> [--out <dest>] [--json]
  ```
  Uploads precompiled binaries, tool artifacts, or datasets directly to the server's central CAS storage tier over streaming gRPC (`UploadBlob`), returning the immutable SHA-256 for use in `.niebla/hooks.toml` without committing binary files to version control.

* **List active workspaces:**
  ```bash
  niebla workspace list [--json]
  ```
  Displays registered workspaces, base changelist revisions, mount paths, and status (`ACTIVE`, `ACTIVE (NO-FUSE)`, or `STALE`).

* **Delete a workspace:**
  ```bash
  niebla workspace delete <name> [--force]
  ```
  Unmounts the FUSE mountpoint, terminates the associated daemon, cleans up local scratch storage, and removes workspace metadata. Use `--force` to delete even if unshelved modifications exist.

---

### Inspecting Changes & `.nieblaignore`

* **Check status:**
  ```bash
  niebla status [--workspace <name>] [--json]
  ```
  Scans the workspace against the base Merkle tree while respecting `.nieblaignore` rules (skipping ignored directories immediately via `filepath.SkipDir`):
  * `[M]  <path>`: Modified file (exists in base tree with changed SHA-256 or executable mode)
  * `[A]  <path>`: Added file (newly created in workspace)
  * `[D]  <path>`: Deleted file (marked with overlayfs whiteout/tombstone in FUSE mode, or removed from disk in `--no-fuse` mode)

* **View unified diff:**
  ```bash
  niebla diff [path] [--workspace <name>]
  ```
  Compares modified/added/deleted workspace files against base CL blobs fetched from local CAS cache or the `niebla-server` via gRPC `ReadBlob`.

---

### Changelist Operations

* **Shelve pending changes:**
  ```bash
  niebla shelve -m "<description>" [--reviewers "alice,bob,@backend"] [--on-cl <parent_cl>] [--cl <id>] [--new-cl] [--workspace <name>] [--json]
  ```
  1. Uploads non-ignored dirty files and compact binary Merkle tree manifests (`pkg/tree`) to server CAS via `UploadBlob(stream)`.
  2. Constructs the updated Merkle tree hierarchy and recomputes hashes up to the root.
  3. Calls `ShelveCL` on `niebla-server` to create or update a shelved changelist:
     - If `--cl <id>` or workspace active CL is set, creates patchset $N+1$ on the existing changelist.
     - **Asynchronous Cascading Rebase:** Re-shelving an upstream parent CL ($B_2$) offloads recursive 3-way Merkle tree re-parenting across downstream shelved children ($C \rightarrow D$) to a background worker queue (`RebaseQueue` with branch-level `KeyedMutex`), allowing `niebla shelve` to return immediately in <100ms.
  4. Expands enterprise groups (e.g. `@backend` -> `alice, bob`) and registers reviewers in `cl_reviewers`.
  5. Records the active `cl_id` and updated Merkle tree in the workspace configuration.

* **Synchronize workspace with upstream changes:**
  ```bash
  niebla sync [--workspace <name>] [--dry-run] [--json]
  ```
  Reconciles the local workspace's base tree with the server's latest rebased tree or trunk:
  * **On-Demand Recursive Sync-Through:** If an author or agent runs `niebla sync` on a downstream child CL ($CL_4$) while background worker re-parenting of upstream parents ($CL_1 \to CL_2 \to CL_3$) is still queued or running, the server synchronously flushes and executes the cascading rebase on-demand from the root down to the target child, guaranteeing 100% deterministic sync results without race conditions.
  * **Clean Sync (FUSE Mode):** If server auto-rebase succeeded without conflict, fast-forwards `root_tree_hash` and sends a `reanchor` IPC request over the Unix domain control socket (`pkg/nieblafs/control`) to `nieblafs.sock`. The daemon purges in-memory metadata/inode caches and re-binds to the new root tree in sub-millisecond time with zero file copy.
  * **Clean Sync (`--no-fuse` Mode):** Directly re-materializes modified/added files into the workspace mount directory (`pkg/materialize`) using cached CAS blobs.
  * **Conflict Handling:** If server detected overlapping edits between parent and child, outputs conflict diagnostics and injects standard conflict markers (`<<<<<<< PARENT`, `=======`, `>>>>>>> CHILD`) into the workspace scratch files for manual resolution.
  * After editing and resolving markers, run `niebla shelve` to commit the rebased patchset.

* **Submit changelist to trunk:**
  ```bash
  niebla submit [--cl <id>] [--workspace <name>] [--no-verify] [--force] [--json]
  ```
  Atomically commits the changelist to trunk head under a transaction-scoped PostgreSQL advisory lock (`pg_advisory_xact_lock`):
  * **Client Presubmit Hooks:** Automatically executes local commands declared in `.niebla/hooks.toml` (`[[client.presubmit]]`) before dialing the server. Pass `--no-verify` to bypass local checks.
  * **Monotonic Trunk Revisions (`trunk_revision`):** While `cl_id` is allocated when a CL is first shelved, `SubmitCL` assigns a strictly sequential `trunk_revision` ($r1, r2, r3, \dots$) in exact commit order so out-of-order CL submissions never invert trunk head.
  * **Automatic 3-Way Trunk Merge:** If a CL's `parent_cl` lags behind the current `HeadCL`, `SubmitCL` automatically performs a server-side 3-way Merkle tree merge (`tree.ThreeWayTreeMerge`). Disjoint changes merge cleanly onto trunk head; overlapping file conflicts abort submission with `FailedPrecondition` (`needs_rebase = true`) so the author can run `niebla sync`.
  * **Submission Guards:** Verifies that required approvals are satisfied (`approvals >= required_approvals`), all affected directory and file scopes in `OWNERS.toml` have received approval from authorized approvers, all server presubmit hooks in `.niebla/hooks.toml` pass (commit description regex, secret patterns, file size limits), no unresolved comments remain when `allow_submit_with_unresolved_comments = false`, CI check runs are green when `require_green_to_submit = true`, and no pending rebase conflicts exist (`needs_rebase = false`).
  * **Bypass Guard:** Use `--force` to bypass unresolved comments, missing approvals, OWNERS policy violations, presubmit hook gates, or failed check runs when urgent trunk submission is required.

### CI Runner Execution & Reporting (`niebla ci`)

* **Wrap & execute a test/build command:**
  ```bash
  niebla ci exec --cl <id> [--patchset <n>] --runner <name> [--server-url <url>] [--json] -- <command> [args...]
  ```
  Runs any build or test command in local or remote container environments, automatically posting `status = "running"`, streaming logs, uploading the log blob to CAS, and reporting `passed` or `failed` back to Cotejo upon completion.

* **Report check status manually:**
  ```bash
  niebla ci report --cl <id> [--patchset <n>] --runner <name> --status <status> [--url <url>] [--log-file <path>] [--json]
  ```
  Reports check run state directly (`queued`, `running`, `passed`, `failed`, `canceled`, `skipped`).


---

## 3. Agent Workflows

### 3.1 Single-CL Task Workflow

```bash
# 1. Create a task workspace (if not already inside one)
niebla workspace create fix-login-auth

# 2. Navigate into workspace mount
cd /niebla/workspaces/fix-login-auth

# 3. Edit, add, or delete files directly
# (Files are written to the CoW scratch overlay automatically)

# 4. Inspect modifications
niebla status
niebla diff

# 5. Shelve changes into a Changelist
niebla shelve -m "auth: fix token expiration handling"

# 6. Submit to trunk
niebla submit
```

### 3.2 Stacked Changelist Workflow (Agentic Multi-CL Pipelines)

When implementing complex multi-part designs, large features, or plan steps agentically, break work into a sequence of small, reviewable changelists where each CL builds upon the previous one ($CL_1 \to CL_2 \to CL_3$):

1. **Provision Isolated Workspaces Per CL:**
   Because Niebla workspaces are zero-clone virtual mounts (<15ms, <100 KB initial disk), never attempt to juggle multiple logical features or stacked CLs inside a single workspace. Provision a dedicated workspace for each step in the chain.

2. **Chaining Dependent Workspaces (`--cl <parent_id>`):**
   * **Step 1 (CL 1):**
     ```bash
     niebla workspace create ws-part1 --json
     cd /niebla/workspaces/ws-part1
     # ... make changes ...
     niebla shelve -m "feat(core): part 1 data models" --json
     # Parse response JSON -> returns "cl_id": 101
     ```
   * **Step 2 (CL 2 stacked on CL 1):**
     Pass `--cl 101` when provisioning the next workspace so it inherits CL 101's exact Merkle tree:
     ```bash
     niebla workspace create ws-part2 --cl 101 --json
     cd /niebla/workspaces/ws-part2
     # ws-part2 immediately sees all files from CL 101 as clean base
     # ... make changes ...
     niebla shelve -m "feat(api): part 2 endpoint implementation" --json
     # Server automatically links ParentCL: 101 -> returns "cl_id": 102
     ```
   * **Step 3 (CL 3 stacked on CL 2):**
     ```bash
     niebla workspace create ws-part3 --cl 102 --json
     cd /niebla/workspaces/ws-part3
     # ... make changes ...
     niebla shelve -m "feat(web): part 3 UI consumption" --json
     # Returns "cl_id": 103 (ParentCL: 102)
     ```

3. **Revising Upstream CLs & The Cascade Rebase Loop:**
   If review feedback or CI requires updates to an upstream changelist (e.g. CL 101):
   * **Update the upstream CL:**
     ```bash
     cd /niebla/workspaces/ws-part1
     # ... apply fixes ...
     niebla shelve -m "feat(core): fix validation logic in models" --json
     # Creates Patchset 2 on CL 101
     ```
   * **Cloud Cascade Rebase:**
     `niebla-server` automatically detects that CL 102 (and downstream CL 103) depend on CL 101. It triggers recursive 3-way Merkle tree merges in the background (`CascadeStackRebase`), synthesizing automated rebase patchsets for clean merges.
   * **Pull Updates in Downstream Workspaces (`niebla sync`):**
     Before making further edits in downstream workspaces, run `niebla sync`:
     ```bash
     cd /niebla/workspaces/ws-part2
     niebla sync --json
     # Triggers on-demand SyncThrough, fast-forwarding ws-part2 to the rebased tree
     ```
   * **Resolving Conflicts:**
     If overlapping edits caused a conflict, `niebla sync` injects standard conflict markers (`<<<<<<< PARENT`, `=======`, `>>>>>>> CHILD`). Edit the files to resolve the markers and run `niebla shelve` to record the resolved patchset.

---

## 4. Cotejo Code Review & Multi-Line Commenting

Cotejo is the web-based code review interface integrated into Niebla. Authors and reviewers collaborate on shelved changelists using side-by-side diffs, patchset comparisons, and granular line/block commenting.

### Accessing the Review UI
* **Web UI URL:** `http://127.0.0.1:7331/cl/<cl_id>` (or live port from `niebla config show`).
* **Patchset Selector:** Switch between patchset versions (e.g. `Patchset 2 vs Patchset 1` or `Patchset 1 vs Base CL`).
* **Stacked Changelist Review:** When viewing a dependent CL (`cl.ParentCL > 0`), Cotejo computes diffs relative to the parent CL's root tree, displaying a clickable `Parent CL #<id>` link in the review header. Reviewers only see the delta introduced by that specific CL. If an upstream rebase introduced conflicts, Cotejo flags *"Rebase Required: Conflicts detected with parent CL"* and disables trunk submission until resolved via `niebla sync`.

### Inline Commenting Mechanics

Cotejo supports both single-line comments and multi-line code block comments:

1. **Single-Line Commenting:**
   - Click the line number or `+` gutter button on any line in the diff.
   - The comment container opens immediately beneath that line with a badge displaying `Line <N>`.

2. **Multi-Line Code Block Commenting:**
   - **Click-and-Drag Selection:** Click and hold the mouse button on a line number, drag vertically across the desired lines (up or down), and release.
   - **Shift-Click Selection:** Click the start line number, hold the `Shift` key, and click the end line number.
   - **Visual Feedback:** All selected lines are highlighted with `.diff-line-selected` during selection, and the active range is styled with `.diff-row-comment-range` (subtle accent left border and glow).
   - **Container Placement:** The comment box is anchored immediately below the ending line of the range, with a badge reading `Lines <start>–<end>`.

3. **Draft Comments & Reply Workflow:**
   - **Save Draft:** The inline comment button is **`Save Draft`** (amber styling). New inline comments are recorded as unposted drafts (`is_draft = true`).
   - **Draft Isolation:** Unposted drafts are strictly private to the author; other reviewers visiting the CL do not see another user's unposted drafts.
   - **Draft Inspection & Discard:** In the inline comment thread, draft comments display an amber `Draft` badge and an inline `Discard` button (`POST /cl/{id}/comments/{id}/discard`) to abandon drafts before publishing.
   - **Real-Time Reply Counter:** The header action bar features a prominent **`Reply`** button displaying `Reply (N)` with an amber badge whenever the reviewer has `N` unposted drafts, dynamically refreshed via HTMX out-of-band updates.
   - **Reply Modal & Publishing:** Clicking `Reply` opens the `<dialog id="reply-modal">`, allowing the reviewer to:
     - Review all pending drafts across files with line range summaries and content previews.
     - Provide an optional top-level review summary / message.
     - Choose a review verdict (`LGTM / Approve`, `Request Changes`, or `Comment Only`).
     - Click `Post Comments` (`POST /cl/{id}/reply`), which atomically publishes all drafts (`is_draft = false`), posts the summary message, registers the verdict, and redirects back to `/cl/{id}`.

4. **Thread Resolution & Approvals:**
   - **Resolving:** Toggle comment status between `Unresolved` and `Resolved` once comments are published.
   - **Approvals:** Submit `LGTM` (Approve) or `Request Changes` directly from the action bar or within the Reply modal dialog.

5. **AI-Generated Comment Hints & Suggested Changes:**
   - **Interactive Hint Generation (`Provide AI hint`):** Checking `🤖 Provide AI hint` in the inline comment box reveals the `✨ Generate Hint` button (`POST /cl/{id}/ai-hint/generate`).
   - **Adaptive Context Aperture:** The `ai-hint-agent` starts with **Tier 1** (`lines`: only the commented line range). If surrounding declarations are required, it automatically widens to **Tier 2** (`block`: enclosing function/type block) or **Tier 3** (`file`: full file up to `max_context_lines`), calling the OpenAI-compatible endpoint configured under `[critique.ai]` with the version-controlled system prompt (`.niebla/prompts/comment_hint.md`).
   - **Dual Suggestion Storage:** Accepting a hint (or an autonomous agent posting a suggestion directly) stores the replacement code in **both** a standard Markdown ```` ```suggestion ```` block inside `comment.body` and dedicated PostgreSQL columns on `cl_comments` (`has_suggestion`, `suggestion_start_line`, `suggestion_end_line`, `suggestion_code`, `suggestion_applied_ps`).
   - **One-Click Patch Application (Submitter):** Published comments with suggestions render a **Suggested Change** diff card with **`✓ Apply Suggestion`** (`POST /cl/{id}/comments/{id}/apply-suggestion`) and **`✕ Reject`** (`POST /cl/{id}/comments/{id}/reject-suggestion`). Clicking **Apply Suggestion** splices the replacement into the file, writes the new blob to CAS, recomputes the Merkle tree, creates **Patchset $N+1$**, and automatically resolves the comment thread (`resolved = true`, `suggestion_applied_ps = N+1`).

### Reviewer Management & Enterprise Groups

Cotejo includes a dedicated **Reviewers Bar** in the header overview:
1. **Assigning Reviewers:**
   - On shelve: `niebla shelve -m "..." --reviewers "alice,bob,@backend"`.
   - In Cotejo UI: Type comma-separated users or `@group` in the `+ reviewer or @group...` input field.
2. **Enterprise User Group Resolution:**
   - Group names prefixed with `@` (e.g. `@backend`) are automatically expanded into their individual group members via the configured group resolver (`static` or `api`).
3. **Verdict Badges:**
   - Each reviewer chip reflects their current status: `⏳ pending` (slate), `✓ lgtm` (emerald), or `! changes_requested` (rose).
4. **Removing Reviewers:**
   - Click the `✕` icon next to an assigned reviewer chip (`POST /cl/{id}/reviewers/{user}/remove`).

### Approval Requirements & Submission Guards

1. **Approval Counter:**
   - When `required_approvals > 0`: Displays a live badge e.g. `Approvals: 1 of 2 required` (amber when pending, emerald checkmark when satisfied).
   - When `required_approvals == 0`: Displays `Approvals: X (optional)`.
2. **Submit CL Button in Cotejo:**
   - Located in the header action bar alongside `Reply` and `LGTM / Approve`.
   - **Approval Checkmarks:** Displays checkmarks matching `required_approvals`. Approved checks inherit the button's font color (`currentColor`), while missing checks are rendered in black.
   - **Ready to Submit:** When all requirements are met, clicking submits the CL to trunk.
   - **Pending Approvals Override:** If `approvedCount < requiredApprovals`, clicking the button opens the `<dialog id="override-approvals-modal">` titled **Override Approvals and Submit**. Users can enter an approval override justification (up to 400 characters with a live character counter); the bottom right Submit button unlocks once any text is entered.
   - **Blocked by Unresolved Comments:** If `allow_submit_with_unresolved_comments = false` and unresolved comments exist, the submit button is styled in amber and clicking it opens the `<dialog id="unresolved-comments-modal">`.
   - **Already Submitted:** Displays an immutable `CL Submitted` status pill.

### Policy & Subtree Governance Engine (`OWNERS.toml`)

Niebla uses a **3-tier enterprise governance architecture** combining host defaults (`~/.config/niebla/*.toml`), a shared repository catalog (`.niebla/agents.toml` and `.niebla/hooks.toml`), and hierarchical directory-level policy files (`OWNERS.toml`, `pkg/owners`) committed directly into the repository Merkle tree:

1. **`OWNERS.toml` Schema:**
   ```toml
   # Approvers authorized to satisfy ownership requirements for this directory
   approvers = [
     "alice",
     "bob",
     "@security-team",
   ]

   # Recommended human reviewers auto-assigned on niebla shelve
   reviewers = [
     "charlie",
   ]

   # Isolate this directory from inheriting parent/root OWNERS approvers and policies
   set_noparent = true

   # Subtree Governance Overrides (optional; inherits/aggregates upward unless set_noparent = true)
   required_approvals = 2
   require_green_ci = true
   allow_unresolved_comments = false

   # Named references to the shared catalog in .niebla/agents.toml, .niebla/hooks.toml, and CI
   agents = ["sec-bot"]
   presubmit_hooks = ["no-raw-sql", "fips-crypto-check"]
   ci_runners = ["security-integration-suite"]

   # Scoped rules for specific file patterns (supports both inline table and legacy array syntax)
   [per_file]
   "*.templ"       = { approvers = ["@design-system"], agents = ["a11y-bot"] }
   "*.proto"       = { approvers = ["@proto-leads"], set_noparent = true, required_approvals = 2 }
   "*.sql"         = { approvers = ["@dba-team"], presubmit_hooks = ["sql-migration-lint"] }
   "BUILD.bazel"   = ["build-cop"]
   ```

2. **Hierarchical Inheritance & Multi-Subtree Aggregation Rules:**
   - When a changelist modifies, adds, or deletes files, `ShelveCL` and `SubmitCL` diff the CL against its base tree (`pkg/tree.DiffTreeFiles`) and evaluate `OWNERS.toml` along each touched file's directory ancestry up to root (`/`), stopping at `set_noparent = true`:
     - **`approvers`:** Every touched directory/per-file scope must have at least one authorized approver (expanding `@group` via the group resolver) who has cast an approving verdict (`lgtm` or `approved`).
     - **`required_approvals`:** Aggregated as the **maximum** `required_approvals` across all touched `OWNERS.toml` chains and matching `[per_file]` rules (falling back to `~/.config/niebla/*.toml` `required_approvals`).
     - **`require_green_ci`:** Evaluates to `true` if **any** touched `OWNERS.toml` or matching `[per_file]` rule sets `require_green_ci = true` or specifies `ci_runners` (falling back to `~/.config/niebla/*.toml` `require_green_check_runs`).
     - **`allow_unresolved_comments`:** Strictest wins (`false` if **any** touched `OWNERS.toml` or `[per_file]` rule sets `allow_unresolved_comments = false`).
     - **`reviewers`, `agents`, `presubmit_hooks`, `ci_runners`:** Deduplicated union across all touched `OWNERS.toml` chains (respecting `set_noparent = true`).
   - Emergency or administrative override is supported via `niebla submit --force` or the Cotejo approval override modal.

### Submit Hook Architecture (`.niebla/hooks.toml`)

Niebla provides an extensible client- and server-side presubmit pipeline configured via `.niebla/hooks.toml` in the repository root (`pkg/hooks`):

1. **`.niebla/hooks.toml` Schema:**
   ```toml
   # Client-Side Presubmit Hooks (run locally in workspace before gRPC dispatch)
   [[client.presubmit]]
   name = "gofmt"
   command = "gofmt -l ."
   fail_on_output = true
   description = "Verify Go source files are formatted"

   # Server-Side Presubmit Hooks (run under pg_advisory_xact_lock in SubmitCL)
   [[server.presubmit]]
   name = "conventional-commits"
   type = "metadata"
   blocking = true
   description = "Enforce Conventional Commits format"
   [server.presubmit.metadata]
   field = "description"
   pattern = "^(feat|fix|docs|refactor|test|chore)(\\(.+\\))?: .+"
   min_length = 10

   [[server.presubmit]]
   name = "forbid-secrets"
   type = "tree_scan"
   blocking = true
   description = "Block credentials and oversized binary files"
   [server.presubmit.tree_scan]
   forbidden_patterns = ["*.pem", "*.key", "id_rsa*"]
   max_file_size_bytes = 10485760 # 10MB limit

   [[server.presubmit]]
   name = "sql-migration-lint"
   type = "cas_binary"
   opt_in = true # Only runs when opted into by a subtree's OWNERS.toml presubmit_hooks = ["sql-migration-lint"]
   cas_sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
   args = ["--strict", "."]
   ```

2. **Execution Flow & Subtree Opt-In:**
   - **Client Execution:** When running `niebla submit`, the CLI executes `[[client.presubmit]]` commands inside the local workspace. Pass `--no-verify` to skip.
   - **Server Execution:** `SubmitCL` resolves `.niebla/hooks.toml` from the CL's root Merkle tree and runs all server-side presubmits matching either global/`trigger_paths` rules OR explicit `presubmit_hooks = [...]` from touched `OWNERS.toml` files (`metadata`, `tree_scan`, `cas_binary`, `in_tree`, `exec`).
   - **Emergency Bypass:** `niebla submit --force` bypasses server-side presubmit gates.

### Review Agent Personas Framework (`.niebla/agents.toml`)

Niebla supports both **AI-powered review personas** (driven by `.niebla/prompts/<domain>.md` and `[critique.ai]`) and **deterministic static analyzers / CAS binaries** declared in `.niebla/agents.toml` (`pkg/agents`, `internal/agents`):

1. **`.niebla/agents.toml` Schema:**
   ```toml
   # AI Persona Agent (uses .niebla/prompts/<domain>.md and [critique.ai] OpenAI endpoint)
   [[agent]]
   name = "a11y-bot"
   display_name = "Accessibility Review Agent"
   type = "ai"
   prompt_file = ".niebla/prompts/accessibility.md"
   trigger_paths = ["internal/web/views/**/*.templ"]
   blocking = false
   auto_approve = false
   capture_logs = true
   post_inline_diagnostics = true
   timeout_seconds = 45

   # Specialized in-tree script reviewer
   [[agent]]
   name = "sec-scanner"
   display_name = "Security Pattern Scanner"
   type = "in_tree"
   entrypoint = "tools/agents/sec-scanner.sh"
   trigger_paths = ["pkg/security/**", "**/auth/**"]
   auto_approve = true            # Casts 'lgtm' if script succeeds (exit 0)
   capture_logs = true            # Ingests stdout/stderr to CAS (/cl/{id}/runs/{agent}/logs)
   post_inline_diagnostics = true # Parses compiler lines OR structured AgentReviewReport JSON
   timeout_seconds = 60

   # Compiled prebuilt binary runner referenced by CAS digest (can be opt_in = true for OWNERS.toml)
   [[agent]]
   name = "package-builder"
   display_name = "Package Build Runner"
   type = "cas_binary"
   cas_sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
   args = ["--strict"]
   trigger_paths = ["src/**", "pkg/**", "go.mod"]
   auto_approve = true
   capture_logs = true
   ```

2. **Automated Assignment & Background Execution Lifecycle:**
   - **Trigger & `OWNERS.toml` Evaluation on Shelve:** Whenever `niebla shelve` is run, `ShelveCL` resolves `OWNERS.toml` (`agents = [...]`) and `.niebla/agents.toml` (`trigger_paths`), auto-assigns matching agents in `cl_reviewers`, materializes the touched files into an isolated workspace, and dispatches `Executor.Execute` asynchronously in the background.
   - **Dual Output Contract (Compiler Diagnostics OR Structured JSON Suggestions):** Agents can emit either standard `path:line:col: message` lines or a structured `AgentReviewReport` JSON containing `suggestion_code`, which automatically populates one-click **`✓ Apply Suggestion`** blocks in Cotejo.
   - **Verdict Voting:** Passing checks automatically grant `lgtm` (when `auto_approve = true`); failing checks register `changes_requested`.

### File Review Tracking & Status

Cotejo tracks the review status of each file in a changelist on a **per-reviewer basis**:

1. **Default State:**
   - Every changed file starts as `not reviewed` for each reviewer.
2. **Automatic Review on Selection:**
   - When a reviewer selects and views a file in Cotejo, that file is automatically transitioned to `reviewed` and persists permanently across sessions.
3. **Per-Reviewer Isolation:**
   - File review states are keyed by `(cl_id, file_path, reviewer)`. When Reviewer A marks a file reviewed, it remains `not reviewed` for Reviewer B.
4. **Visual Progress & Manual Toggling:**
   - **Header Progress:** The overview header displays `X of Y reviewed by <reviewer>` with live counts.
   - **Interactive HTMX Badges:** The changed files table displays a status pill for each file:
     - `reviewed` (emerald badge with checkmark)
     - `not reviewed` (muted slate badge)
   - Clicking the pill badge instantly toggles between `reviewed` and `not reviewed` without a full page reload.
5. **Dynamic User Identity:**
   - Defaults dynamically to the active OS username (`$USER`), configured via `[critique.auth]` in `~/.config/niebla/config.toml`.
   - Emulate or test different reviewers by passing `?reviewer=<username>` in any Cotejo URL.

### Review Inbox Segregation & Agent Queues

To prevent review fatigue and protect human attention from high-volume agent swarms, Cotejo automatically partitions the review inbox (`/` and `/cls`) into segregated tabs:

* **`My Review Queue` (`?tab=my-reviews`):**
  Changelists explicitly assigned to the authenticated user or one of their enterprise groups (`@team`) as reviewers. Only this queue triggers high-priority review attention.
* **`Out for Review` (`?tab=outgoing`):**
  Pending changelists authored by the authenticated user awaiting review or submit approval.
* **`Agent / Automated` (`?tab=agents`):**
  Automated background agent tasks, linter suggestions, dependency bot updates, and CI branch tasks (matching `agent_author_patterns` such as `agent/*`, `bot/*`, `*[bot]`, `*@agent*`, `ci/*`, `system`). Agent CLs are marked with a `🤖 Agent` badge and segregated from the primary human review queue.
* **`Submitted / Trunk` (`?tab=submitted`):**
  Changelists merged cleanly into trunk revisions.
* **`All` (`?tab=all`):**
  Unfiltered stream of all changelists across the repository.

### Programmatic HTMX / REST API

Agents interacting with Cotejo via HTTP can utilize the following endpoints:

| Method | Endpoint | Parameters | Description |
| :--- | :--- | :--- | :--- |
| `GET` | `/cls` | `tab=my-reviews\|outgoing\|agents\|submitted\|all&reviewer={user}` | Segregated review inbox; returns partial content when `HX-Request: true`. |
| `GET` | `/cl/{id}` | `file={path}&ps={ps}&base_ps={base}&reviewer={user}` | Full review interface; auto-marks selected file reviewed and displays active reviewer's drafts. |
| `GET` | `/cl/{id}/comment-box` | `file={path}&line={start}&end_line={end}&side={side}&ps={ps}` | Fetches inline comment thread container. |
| `POST` | `/cl/{id}/comments` | `file_path, line_number, end_line_number, side, author, body, has_suggestion, suggestion_start_line, suggestion_end_line, suggestion_code` | Creates single or multi-line comment as an unposted draft (`is_draft=true`) with optional dual-stored code suggestion. |
| `POST` | `/cl/{id}/ai-hint/generate` | `cl_id, patchset_number, file_path, line_number, end_line_number, body` | Queries `ai-hint-agent` (OpenAI-compatible endpoint with adaptive context aperture) and returns `AIHintPreviewCard`. |
| `POST` | `/cl/{id}/comments/{id}/apply-suggestion` | *(none)* | Applies suggested code block to latest patchset, writes blob to CAS, creates Patchset $N+1$, and auto-resolves the thread. |
| `POST` | `/cl/{id}/comments/{id}/reject-suggestion` | *(none)* | Marks the code suggestion on the comment as rejected (`suggestion_applied_ps = -1`). |
| `POST` | `/cl/{id}/comments/{id}/discard` | `author={user}` | Discards a single unposted draft comment. |
| `POST` | `/cl/{id}/reply` | `author={user}&message={msg}&verdict=lgtm\|changes_requested\|none` | Publishes all pending draft comments for reviewer, posts summary message, and records verdict. |
| `POST` | `/cl/{id}/comments/{id}/resolve` | `resolved=true\|false` | Toggles resolution status of a published comment. |
| `POST` | `/cl/{id}/approval` | `verdict=lgtm\|changes_requested&reviewer={user}` | Records review verdict. |
| `POST` | `/cl/{id}/reviewers` | `reviewer={name\|@group}` | Assigns one or more reviewers with group expansion. |
| `POST` | `/cl/{id}/reviewers/{user}/remove` | *(none)* | Unassigns a reviewer from the CL. |
| `POST` | `/cl/{id}/submit` | *(none)* | Commits CL to trunk if approvals and unresolved comments policy guards pass. |
| `POST` | `/cl/{id}/file-review` | `file={path}&reviewed=true\|false&reviewer={user}&ps={ps}&base_ps={base}` | Toggles per-reviewer file review status and returns updated badge HTML. |

---

## 5. Disaster Recovery, Backup & Replication (`niebla backup`)

Niebla includes an enterprise-grade, completely decoupled resilience and replication suite designed for mission-critical VCS repositories with legal compliance and tamper-evident provenance requirements.

### Architecture Highlights
1. **Zero VCS Coupling:** Replication and backup processes run independently and never block developers or fail core VCS workflows if network/remote storage goes offline.
2. **Native ZFS 10GbE Snapshot Streaming (Default):**
   - High-throughput block-level streaming directly across dedicated 10GbE LAN links via `zfs send -w | ssh zfs recv -F`.
   - Raw dataset encryption (`-w`) preserves dataset-level encryption without decrypting on the source or re-encrypting across the wire.
3. **Standalone Encrypted Bundles (`.nieblabak`):**
   - Standard tar archives compressed with zstd and encrypted using AES-256-GCM or ChaCha20-Poly1305 envelope encryption.
   - Each bundle contains an authenticated cryptographic header (`NIEBLAK\x01`), ephemeral DEK wrapped with the Master KEK, Argon2id KDF salt, and SHA-256 integrity verifications.
4. **Multi-Target Storage Drivers:**
   - **ZFS Pool:** Native block-level streaming.
   - **Filesystem / 10G Mount:** High-throughput streaming with 8MB buffer pools.
   - **Nextcloud WebDAV:** Seamless synchronization into personal Nextcloud storage.
   - **S3 Protocol Stores:** Direct AWS SigV4 signed uploads to AWS S3, MinIO, Cloudflare R2, Ceph, and Wasabi without heavy cloud SDK dependencies.

### Command Quick Reference

| Command | Description |
| :--- | :--- |
| `niebla backup create [--mode <zfs\|bundle>] [--target <zfs\|filesystem\|nextcloud\|s3>] [--label <label>]` | Creates a new ZFS recursive snapshot or envelope-encrypted standalone bundle archive. |
| `niebla backup list [--target <target>] [--dir <path>]` | Lists historical snapshots or remote backup archives with sizes and timestamps. |
| `niebla backup verify <file\|snapshot> [--target <target>]` | Verifies cryptographic AEAD headers, DEK unwrap, and snapshot validity without unpacking. |
| `niebla backup restore <file\|snapshot> [--dry-run] [--dest <dir>]` | Restores dataset from ZFS snapshot or decrypts and unpacks `.nieblabak` bundle. `--dry-run` validates safety without disk writes. |
| `niebla backup prune [--keep <count>] [--target <target>]` | Enforces retention policy by safely destroying older snapshots or deleting aged archives. |

### Operational Examples

* **Create a native ZFS snapshot and replicate over 10G link:**
  ```bash
  niebla backup create --mode zfs --label daily
  ```

* **Create an encrypted bundle and upload to Nextcloud:**
  ```bash
  export NIEBLA_MASTER_KEY="<32-byte-hex-key>"
  export NEXTCLOUD_APP_PASSWORD="<app-password>"
  niebla backup create --mode bundle --target nextcloud --label offsite
  ```

* **Perform a non-destructive dry-run restore validation:**
  ```bash
  niebla backup restore niebla-daily-20261025.nieblabak --dry-run
  ```

* **Restore an archive into an isolated verification directory:**
  ```bash
  niebla backup restore niebla-daily-20261025.nieblabak --dest ./disaster-recovery-test
  ```

* **Prune older snapshots beyond retention policy (keep 7):**
  ```bash
  niebla backup prune --keep 7 --target zfs
  ```

---

## 7. Related Skills

* [niebla-ci](file:///home/matatan/.gemini/config/skills/niebla-ci/SKILL.md): Continuous integration runner configuration (`[ci.runners.<name>]`), workflow authoring (`niebla export` + `niebla ci exec`), and check run troubleshooting.
* [niebla-config](file:///home/matatan/.gemini/config/skills/niebla-config/SKILL.md): Configuration schema reference (`~/.config/niebla/config.toml`), theming presets, and fast-fail validation.
* [niebla-review-agent](file:///home/matatan/.gemini/config/skills/niebla-review-agent/SKILL.md): Autonomous AI code review agent workflows, adaptive context aperture, inline ```` ```suggestion ```` commenting, and `.niebla/agents.toml` registration.
