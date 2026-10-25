---
name: citc-vcs
description: >-
  Workflows, command syntax, and best practices for interacting with the CitC (Client in the Cloud)
  version control system and Critique code review UI. Use when managing virtual workspaces, inspecting CoW scratch changes,
  diffing against base CL blobs, shelving changelists, submitting to trunk, and conducting code reviews with single-line or multi-line code block comments.
---

# CitC Version Control System Runbook

CitC is a server-authoritative, cloud-native monorepo VCS inspired by Google Piper and CitC (strictly no Git). Workspaces are virtual, read-only FUSE mounts backed by a Copy-on-Write (CoW) scratch overlay and a Content-Addressable Storage (CAS) backend.

---

## 1. Core Principles for AI Agents

1. **Strictly No Git:** Do NOT use `git checkout`, `git add`, `git commit`, `git push`, or `git status`. All version control operations MUST be performed via `citc`.
2. **Virtual Workspace Mounts:** Workspaces live in `~/citc/<name>`. Multiple workspaces run side-by-side concurrently without switching branches in-place.
3. **Copy-on-Write (CoW) Scratch:** Edits made inside `~/citc/<name>` are stored in the local scratch overlay (`/citc/workspaces/<name>/scratch` or `~/.citc/workspaces/<name>/scratch`).
4. **Automatic Workspace Detection:** Running `citc` commands inside `~/citc/<name>` automatically detects the active workspace via `$PWD`. When running from outside, specify `--workspace <name>`.
5. **Code Review with Critique:** Code reviews and changelist inspections are conducted in Critique (`http://127.0.0.1:7331` or configured port), featuring side-by-side diffs, patchset history, and multi-line code block comments.

---

## 2. Command Reference

### Workspace Lifecycle

* **Create a workspace:**
  ```bash
  citc workspace create <name> [--cl <base_cl>] [--mount-dir <path>]
  ```
  Creates a workspace on the server (bound to head CL by default or `--cl <id>`), provisions scratch and cache directories, and mounts the virtual filesystem at `~/citc/<name>`.

* **List active workspaces:**
  ```bash
  citc workspace list
  ```
  Displays a table of registered workspaces, base changelist revisions, mount paths, and status (`ACTIVE` / `STALE`).

* **Delete a workspace:**
  ```bash
  citc workspace delete <name> [--force]
  ```
  Unmounts the FUSE mountpoint, terminates the associated daemon, cleans up local scratch storage, and removes workspace metadata. Use `--force` to delete even if unshelved modifications exist.

---

### Inspecting Changes

* **Check status:**
  ```bash
  citc status [--workspace <name>]
  ```
  Scans the scratch overlay against the base Merkle tree:
  * `[M]  <path>`: Modified file (exists in base tree, edited in scratch)
  * `[A]  <path>`: Added file (newly created in scratch)
  * `[D]  <path>`: Deleted file (marked with overlayfs whiteout or tombstone)

* **View unified diff:**
  ```bash
  citc diff [path] [--workspace <name>]
  ```
  Compares modified/added/deleted scratch files against base CL blobs fetched from local CAS cache or the `citc-server` via gRPC `ReadBlob`.

---

### Changelist Operations

* **Shelve pending changes:**
  ```bash
  citc shelve -m "<description>" [--reviewers "alice,bob,@backend"] [--on-cl <parent_cl>] [--workspace <name>]
  ```
  1. Uploads dirty files to server CAS via `UploadBlob(stream)`.
  2. Constructs the updated Merkle tree hierarchy and recomputes hashes up to the root.
  3. Calls `ShelveCL` on `citc-server` to create a shelved changelist.
  4. Expands enterprise groups (e.g. `@backend` -> `alice, bob`) and registers reviewers in `cl_reviewers`.
  5. Records the newly assigned `cl_id` as active in the workspace configuration.

* **Submit changelist to trunk:**
  ```bash
  citc submit [--cl <id>] [--workspace <name>] [--force]
  ```
  Atomically commits the changelist to trunk head:
  * **Submission Guards:** Verifies that required approvals are satisfied (`approvals >= required_approvals`) and that no unresolved comments remain when `allow_submit_with_unresolved_comments = false`.
  * **Bypass Guard:** Use `--force` to bypass unresolved comments or missing approvals when urgent trunk submission is required.

---

## 3. Standard Agent Workflow

```bash
# 1. Create a task workspace (if not already inside one)
citc workspace create fix-login-auth

# 2. Navigate into workspace mount
cd ~/citc/fix-login-auth

# 3. Edit, add, or delete files directly
# (Files are written to the CoW scratch overlay automatically)

# 4. Inspect modifications
citc status
citc diff

# 5. Shelve changes into a Changelist
citc shelve -m "auth: fix token expiration handling"

# 6. Submit to trunk
citc submit
```

---

## 4. Critique Code Review & Multi-Line Commenting

Critique is the web-based code review interface integrated into CitC. Authors and reviewers collaborate on shelved changelists using side-by-side diffs, patchset comparisons, and granular line/block commenting.

### Accessing the Review UI
* **Web UI URL:** `http://127.0.0.1:7331/cl/<cl_id>` (or live port from `citc config show`).
* **Patchset Selector:** Switch between patchset versions (e.g. `Patchset 2 vs Patchset 1` or `Patchset 1 vs Base CL`).

### Inline Commenting Mechanics

Critique supports both single-line comments and multi-line code block comments:

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

### Reviewer Management & Enterprise Groups

Critique includes a dedicated **Reviewers Bar** in the header overview:
1. **Assigning Reviewers:**
   - On shelve: `citc shelve -m "..." --reviewers "alice,bob,@backend"`.
   - In Critique UI: Type comma-separated users or `@group` in the `+ reviewer or @group...` input field.
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
2. **Submit CL Button in Critique:**
   - Located in the header action bar alongside `Reply` and `LGTM / Approve`.
   - **Active (Sky Blue):** Ready to submit (`cl.Status == "shelved"` and requirements met). Submitting commits the CL to trunk and updates the status badge to `Submitted`.
   - **Blocked by Unresolved Comments:** If `allow_submit_with_unresolved_comments = false` and unresolved comments exist, the submit button is styled in amber. Clicking it opens the `<dialog id="unresolved-comments-modal">`, explaining the policy and listing all affected files with quick navigation links.
   - **Pending Approvals:** If `approvedCount < requiredApprovals`, the submit button is disabled with a descriptive tooltip e.g. `"Requires 2 approval(s) before submission"`.
   - **Already Submitted:** Displays an immutable `CL Submitted` status pill.

### File Review Tracking & Status

Critique tracks the review status of each file in a changelist on a **per-reviewer basis**:

1. **Default State:**
   - Every changed file starts as `not reviewed` for each reviewer.
2. **Automatic Review on Selection:**
   - When a reviewer selects and views a file in Critique, that file is automatically transitioned to `reviewed` and persists permanently across sessions.
3. **Per-Reviewer Isolation:**
   - File review states are keyed by `(cl_id, file_path, reviewer)`. When Reviewer A marks a file reviewed, it remains `not reviewed` for Reviewer B.
4. **Visual Progress & Manual Toggling:**
   - **Header Progress:** The overview header displays `X of Y reviewed by <reviewer>` with live counts.
   - **Interactive HTMX Badges:** The changed files table displays a status pill for each file:
     - `reviewed` (emerald badge with checkmark)
     - `not reviewed` (muted slate badge)
   - Clicking the pill badge instantly toggles between `reviewed` and `not reviewed` without a full page reload.
5. **Dynamic User Identity:**
   - Defaults dynamically to the active OS username (`$USER`), configured via `[critique.auth]` in `~/.config/citc-toolchain/config.toml`.
   - Emulate or test different reviewers by passing `?reviewer=<username>` in any Critique URL.

### Programmatic HTMX / REST API

Agents interacting with Critique via HTTP can utilize the following endpoints:

| Method | Endpoint | Parameters | Description |
| :--- | :--- | :--- | :--- |
| `GET` | `/cl/{id}` | `file={path}&ps={ps}&base_ps={base}&reviewer={user}` | Full review interface; auto-marks selected file reviewed and displays active reviewer's drafts. |
| `GET` | `/cl/{id}/comment-box` | `file={path}&line={start}&end_line={end}&side={side}&ps={ps}` | Fetches inline comment thread container. |
| `POST` | `/cl/{id}/comments` | `file_path, line_number, end_line_number, side, author, body` | Creates single or multi-line comment as an unposted draft (`is_draft=true`). |
| `POST` | `/cl/{id}/comments/{id}/discard` | `author={user}` | Discards a single unposted draft comment. |
| `POST` | `/cl/{id}/reply` | `author={user}&message={msg}&verdict=lgtm\|changes_requested\|none` | Publishes all pending draft comments for reviewer, posts summary message, and records verdict. |
| `POST` | `/cl/{id}/comments/{id}/resolve` | `resolved=true\|false` | Toggles resolution status of a published comment. |
| `POST` | `/cl/{id}/approval` | `verdict=lgtm\|changes_requested&reviewer={user}` | Records review verdict. |
| `POST` | `/cl/{id}/reviewers` | `reviewer={name\|@group}` | Assigns one or more reviewers with group expansion. |
| `POST` | `/cl/{id}/reviewers/{user}/remove` | *(none)* | Unassigns a reviewer from the CL. |
| `POST` | `/cl/{id}/submit` | *(none)* | Commits CL to trunk if approvals and unresolved comments policy guards pass. |
| `POST` | `/cl/{id}/file-review` | `file={path}&reviewed=true\|false&reviewer={user}&ps={ps}&base_ps={base}` | Toggles per-reviewer file review status and returns updated badge HTML. |


