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
  citc shelve -m "<description>" [--on-cl <parent_cl>] [--workspace <name>]
  ```
  1. Uploads dirty files to server CAS via `UploadBlob(stream)`.
  2. Constructs the updated Merkle tree hierarchy and recomputes hashes up to the root.
  3. Calls `ShelveCL` on `citc-server` to create a shelved changelist.
  4. Records the newly assigned `cl_id` as active in the workspace configuration.

* **Submit changelist to trunk:**
  ```bash
  citc submit [--cl <id>] [--workspace <name>]
  ```
  Atomically commits the changelist to trunk head. If `--cl` is omitted, submits the active shelved CL in the current workspace.

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

3. **Submitting & Managing Comments:**
   - **Thread Replies:** Reviewers and authors reply within the same comment thread container.
   - **Resolving:** Toggle comment status between `Unresolved` and `Resolved`.
   - **Approvals:** Submit `LGTM` (Approve) or `Needs Work` directly from the action bar.

### Programmatic HTMX / REST API

Agents interacting with Critique via HTTP can utilize the following endpoints:

| Method | Endpoint | Parameters | Description |
| :--- | :--- | :--- | :--- |
| `GET` | `/cl/{id}` | `file={path}&ps={ps}&base_ps={base}` | Full review interface. |
| `GET` | `/cl/{id}/comment-box` | `file={path}&line={start}&end_line={end}&side={side}&ps={ps}` | Fetches inline comment thread container. |
| `POST` | `/cl/{id}/comments` | `file_path, line_number, end_line_number, side, author, body` | Creates single or multi-line comment. |
| `POST` | `/cl/{id}/comments/{id}/resolve` | `resolved=true\|false` | Toggles resolution status. |
| `POST` | `/cl/{id}/approve` | `verdict=lgtm\|needs_work` | Records review verdict. |

