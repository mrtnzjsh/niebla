---
title: "Cotejo HTTP & REST API Reference"
description: Complete reference for Cotejo's REST APIs, HTMX endpoints, CI status hooks, review approvals, and AI suggestion patchset synthesis.
sidebar:
  order: 5
  label: Cotejo HTTP & REST API
---

**Cotejo** exposes a dual-purpose HTTP interface on port `8080`:
1. **Interactive HTMX & Server-Sent Events (SSE)** for fast, responsive web browsing without heavyweight single-page application (SPA) overhead.
2. **Deterministic REST & JSON Endpoints** for CI orchestrators (`niebla ci`), automated review personas, and developer tooling.

---

## 1. Authentication & Base URL

The default server binds to:
```text
http://127.0.0.1:8080
```

### Identity Resolution Profiles
Requests are authenticated according to the active `[critique.auth]` profile in `~/.config/niebla/config.toml`:

| Profile Mode | Header / Credential | Resolution Logic |
| :--- | :--- | :--- |
| `developer` (default) | Optional `X-Niebla-User` header | Uses `critique.auth.default_user` (e.g. `matatan`) if omitted. |
| `proxy_header` | Header specified in config (e.g. `X-Forwarded-User` or `Tailscale-User-Login`) | Injected by trusted reverse proxy (NGINX, Traefik, Tailscale). |
| `oidc` | `Authorization: Bearer <jwt>` | Cryptographically validated against configured OIDC identity provider. |

---

## 2. Changelist Navigation & Diff Rendering

### `GET /` & `GET /cls`
Fetches the Cotejo review inbox, partitioned into active attention sets:
* **Attention Set (Needs Review):** Changelists waiting for your review.
* **Outgoing CLs (Needs Attention):** Your shelved changelists awaiting reviewer responses or revisions.
* **Automated CI / Bot CLs:** Segregated automated changelists.

#### Query Parameters
* `status` *(optional)*: Filter by changelist state (`active`, `submitted`, `abandoned`).
* `format=json` *(optional)*: Returns structured JSON instead of HTML.

---

### `GET /cl/{id}`
Renders the changelist detail page, including author metadata, current patchset, attention sets, `OWNERS.toml` approvals status, and the modified file tree.

```bash
curl -s http://127.0.0.1:8080/cl/104
```

---

### `GET /cl/{id}/file/{path...}`
Renders the side-by-side or unified diff for a single file within the changelist.

#### Query Parameters
* `base_ps` *(optional)*: Left-hand patchset number (default: `0`, representing base trunk parent).
* `mod_ps` *(optional)*: Right-hand patchset number (default: latest shelved patchset).
* `view` *(optional)*: `split` (two-pane) or `unified` (single-pane).

---

### `GET /cl/{id}/diff-content`
HTMX partial endpoint returning only the syntax-highlighted diff chunk container for dynamic file expansion.

#### Query Parameters
* `file`: Path to the modified file.
* `left`: Left-hand patchset number.
* `right`: Right-hand patchset number.

---

## 3. Code Review & Approval Endpoints

### `POST /cl/{id}/approval`
Records or revokes a code review approval (LGTM) on the specified changelist.

#### Request Parameters (Form-Encoded)
| Field | Type | Description |
| :--- | :--- | :--- |
| `action` | string | `approve` (grants LGTM) or `unapprove` (revokes approval). |
| `reviewer` | string | Explicit reviewer username (defaults to authenticated identity). |

#### Response
Returns updated HTML for the action bar with `HX-Trigger: approval-updated` to notify connected SSE listeners.

```bash
curl -X POST http://127.0.0.1:8080/cl/104/approval \
  -d "action=approve"
```

---

### `POST /cl/{id}/file-review`
Toggles the per-reviewer "Reviewed" status on an individual file.

#### Request Parameters (Form-Encoded)
| Field | Type | Description |
| :--- | :--- | :--- |
| `file` | string | File path (e.g. `pkg/cas/gc.go`). |
| `reviewed` | boolean | `true` or `false`. |

---

### `POST /cl/{id}/reviewers`
Adds one or more human reviewers or owner groups to the changelist attention set.

#### Request Parameters (Form-Encoded)
| Field | Type | Description |
| :--- | :--- | :--- |
| `reviewers` | string | Comma-separated list of usernames or `@group` identifiers (e.g. `matatan, @security-reviewers`). |

---

### `POST /cl/{id}/reviewers/{user}/remove` & `DELETE /cl/{id}/reviewers/{user}`
Removes a reviewer from the changelist attention set.

---

### `POST /cl/{id}/reply` & `POST /cl/{id}/publish`
Atomically publishes all pending draft comments for the authenticated reviewer, posts an overall message, and updates the attention set.

#### Request Parameters (Form-Encoded)
| Field | Type | Description |
| :--- | :--- | :--- |
| `message` | string | Optional overall review comment. |
| `verdict` | string | Optional verdict: `lgtm`, `needs_work`, or `comment`. |

---

### `POST /cl/{id}/submit`
Submits an approved changelist onto the monotonic trunk head.

#### Precondition Validations
1. **Approval Gates:** Must satisfy minimum required approvals and mandatory subtree owners per `.niebla/OWNERS.toml`.
2. **Unresolved Comments:** Must have 0 open threads unless `allow_submit_with_unresolved_comments = true`.
3. **CI Check Runs:** All required CI runners must be in a passing (`passed`) state.
4. **Trunk Freshness:** Base tree must match current trunk head (or triggers cascading rebase).

#### Request Parameters (Form-Encoded)
| Field | Type | Description |
| :--- | :--- | :--- |
| `force` | boolean | Bypasses non-blocking warnings (if authorized). |
| `justification` | string | Mandatory audit justification string when overriding approval gates. |

```bash
curl -X POST http://127.0.0.1:8080/cl/104/submit
```

---

## 4. Inline Comments & AI Code Hints

### `GET /cl/{id}/comment-box`
Returns the HTMX form component for adding an inline comment on a specific line or line range.

#### Query Parameters
* `file`: Target file path.
* `line`: Starting line number.
* `end_line`: Ending line number (for multi-line range selection).
* `side`: `base` (unmodified left) or `mod` (modified right).

---

### `POST /cl/{id}/comments`
Creates a new draft or published comment, with optional suggested replacement code.

#### Request Parameters (Form-Encoded or JSON)
| Field | Type | Description |
| :--- | :--- | :--- |
| `file_path` | string | Target file path. |
| `line_number` | int | Anchor line number. |
| `end_line_number` | int | Optional end line for multi-line block comments. |
| `side` | string | `base` or `mod`. |
| `body` | string | Markdown-formatted comment text. |
| `is_draft` | boolean | `true` (default) to save as private draft; `false` to publish immediately. |
| `suggestion_code` | string | Optional replacement code block for one-click author application. |

---

### `POST /cl/{id}/comments/{comment_id}/resolve`
Marks a comment thread as resolved (`resolved=true`) or reopens it (`resolved=false`).

---

### `POST /cl/{id}/comments/{comment_id}/discard`
Deletes an unpublished draft comment.

---

### `POST /cl/{id}/ai-hint/generate`
Invokes the configured AI review model or review agent to inspect the selected code range and generate an automated explanation and suggested replacement patch.

#### Request Parameters (Form-Encoded)
| Field | Type | Description |
| :--- | :--- | :--- |
| `file` | string | Target file path. |
| `line` | int | Anchor line number. |
| `end_line` | int | End line number. |
| `side` | string | `base` or `mod`. |
| `prompt` | string | Optional custom instruction for the AI model. |

#### Response Format
Returns an HTMX comment box prepopulated with the AI model's critique and a structured suggestion block:
````markdown
```suggestion
func (s *Storage) Put(ctx context.Context, r io.Reader) (string, error) {
    return s.putChunked(ctx, r, DefaultChunkSize)
}
```
````

---

### `POST /cl/{id}/comments/{comment_id}/apply-suggestion`
Applies an AI or peer code suggestion directly to the repository:
1. Rewrites the modified file in the latest Merkle tree with the suggested replacement lines.
2. Synthesizes a new patchset (`Patchset N+1`) in `cl_patchsets`.
3. Marks `suggestion_applied_ps = N+1` on the comment record.
4. Returns the updated diff view.

```bash
curl -X POST http://127.0.0.1:8080/cl/104/comments/482/apply-suggestion
```

---

### `POST /cl/{id}/comments/{comment_id}/reject-suggestion`
Marks the suggestion as rejected without applying code modifications.

---

## 5. Continuous Integration (CI) Check Runs API

Cotejo provides native REST endpoints for CI runners and webhook dispatchers to report build, test, and lint statuses.

### `POST /api/v1/ci/status`
Reports or updates the status of an external or local check run.

#### JSON Request Body
```json
{
  "cl_id": 104,
  "patchset": 2,
  "runner_name": "nix-check",
  "provider": "forgejo",
  "status": "passed",
  "url": "https://git.internal/actions/runs/8412",
  "log_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
}
```

#### Fields
| Field | Type | Requirement | Description |
| :--- | :--- | :--- | :--- |
| `cl_id` | integer | Required | Changelist ID. |
| `patchset` | integer | Required | Target patchset revision number. |
| `runner_name` | string | Required | Runner identifier matching `OWNERS.toml` required checks. |
| `provider` | string | Optional | CI driver name (`local`, `forgejo`, `github`, `webhook`). |
| `status` | string | Required | `queued`, `running`, `passed`, `failed`, `canceled`, `skipped`. |
| `url` | string | Optional | External link to CI build logs or pipeline dashboard. |
| `log_hash` | string | Optional | SHA-256 digest of hermetic build log artifact in CAS. |

#### CLI Convenience Command
CI runners typically call this endpoint using the unified CLI:
```bash
niebla ci report \
  --cl 104 \
  --patchset 2 \
  --runner "nix-check" \
  --status "passed" \
  --url "https://git.internal/actions/runs/8412"
```

---

### `GET /api/v1/ci/status` & `GET /cl/{id}/checks`
Fetches all check runs recorded for a changelist and patchset.

#### Query Parameters
* `cl_id` (or in path `/cl/{id}/checks`): Changelist ID.
* `patchset` *(optional)*: Filter by patchset revision number.

#### JSON Response
```json
[
  {
    "id": 91,
    "cl_id": 104,
    "patchset": 2,
    "runner_name": "nix-check",
    "provider": "forgejo",
    "status": "passed",
    "url": "https://git.internal/actions/runs/8412",
    "log_hash": "e3b0c442...",
    "created_at": "2026-11-14T02:15:00Z",
    "updated_at": "2026-11-14T02:17:30Z"
  }
]
```

---

## 6. Raw Content Streaming & Diagnostics

### `GET /cl/{id}/raw`
Streams the unrendered, raw contents of a file directly from CAS storage.

#### Query Parameters
* `file`: Relative repository path.
* `side`: `base` (unmodified parent) or `mod` (modified patchset).
* `ps` *(optional)*: Explicit patchset number.

---

### `GET /raw`
Streams an arbitrary blob directly by its SHA-256 content address.

#### Query Parameters
* `hash`: 64-character hexadecimal SHA-256 hash.

```bash
curl -s "http://127.0.0.1:8080/raw?hash=4f8b2d18c991a..." --output file.bin
```

---

### `GET /healthz`
Service liveness and readiness probe for load balancers and systemd supervisors.

#### Response
```text
HTTP/1.0 200 OK
Content-Type: text/plain

ok
```

