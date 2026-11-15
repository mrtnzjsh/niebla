---
title: "Cotejo HTTP & REST API"
description: Reference for Cotejo's HTTP endpoints, HTMX diff partials, review approvals, inline suggestions, and CI check run webhooks.
sidebar:
  order: 5
---

**Cotejo** serves both an interactive web interface (powered by [HTMX](https://en.wikipedia.org/wiki/Htmx) and [Server-Sent Events](https://en.wikipedia.org/wiki/Server-sent_events)) and JSON REST endpoints on port `8080` (`http://127.0.0.1:8080` by default).

---

## 1. Authentication

Requests are authenticated according to `[critique.auth]` in `~/.config/niebla/config.toml` (see [Deployment & Authentication](/niebla/cotejo/deployment-auth/)):

| Auth Mode (`critique.auth.mode`) | Identity Source | Description |
| :--- | :--- | :--- |
| `local` (default) | `critique.auth.default_user` (`$USER`) | Single-user local workstation mode. |
| `tailscale` | `/run/tailscale/tailscaled.sock` | Resolves peer identity automatically via Tailscale WhoIs. |
| `headers` | `critique.auth.header_name` (e.g., `X-Forwarded-User`) | Trusts identity headers injected by [CIDR](https://en.wikipedia.org/wiki/Classless_Inter-Domain_Routing) ranges in `trusted_proxies`. |

---

## 2. Changelist & Diff Endpoints

| Method & Path | Query Parameters | Description |
| :--- | :--- | :--- |
| `GET /` or `GET /cls` | `status` (`active`, `submitted`, `abandoned`), `format=json` | Renders the review dashboard or returns the CL list as JSON. |
| `GET /cl/{id}` | — | Renders the changelist overview, attention set, and modified file tree. |
| `GET /cl/{id}/file/{path...}` | `base_ps` (`0` = trunk), `mod_ps` (latest), `view` (`split` or `unified`) | Renders the side-by-side or unified diff for a single file. |
| `GET /cl/{id}/diff-content` | `file`, `left`, `right` | HTMX partial returning syntax-highlighted diff hunks between two patchsets. |
| `GET /cl/{id}/raw` | `file`, `side` (`base` or `mod`), `ps` | Streams raw file contents for a specific changelist patchset. |
| `GET /raw` | `hash` (64-char hex [SHA-256](https://en.wikipedia.org/wiki/SHA-2)) | Streams an arbitrary blob directly from [CAS](https://en.wikipedia.org/wiki/Content-addressable_storage) by SHA-256 digest. |
| `GET /healthz` | — | Returns `200 OK` (`ok`) for load balancer and `systemd` health checks. |

---

## 3. Review & Approval Endpoints

### `POST /cl/{id}/approval`

Grants or revokes an `LGTM` approval on the changelist.

| Form Field | Type | Description |
| :--- | :--- | :--- |
| `action` | `string` | `approve` (grants LGTM) or `unapprove` (revokes approval). |
| `reviewer` | `string` | Optional reviewer override (when permitted by auth configuration). |

```bash
curl -X POST http://127.0.0.1:8080/cl/104/approval -d "action=approve"
```

---

### `POST /cl/{id}/reply` & `POST /cl/{id}/publish`

Atomically publishes all pending draft comments for the current reviewer, posts an optional summary message, and updates the attention set.

| Form Field | Type | Description |
| :--- | :--- | :--- |
| `message` | `string` | Optional top-level review summary message. |
| `verdict` | `string` | Optional verdict: `lgtm`, `needs_work`, or `comment`. |

---

### `POST /cl/{id}/reviewers` & `DELETE /cl/{id}/reviewers/{user}`

Adds or removes human reviewers or `@group` aliases from the changelist attention set.

| Form Field | Type | Description |
| :--- | :--- | :--- |
| `reviewers` | `string` | Comma-separated usernames or `@group` identifiers (e.g., `alice,@security`). |

---

### `POST /cl/{id}/file-review`

Marks a file as reviewed or unreviewed for the current user.

| Form Field | Type | Description |
| :--- | :--- | :--- |
| `file` | `string` | Repository-relative file path. |
| `reviewed` | `boolean` | `true` or `false`. |

---

### `POST /cl/{id}/submit`

Submits an approved changelist to the linear trunk once all governance gates (`OWNERS.toml`, unresolved comments, and required CI checks) pass.

| Form Field | Type | Description |
| :--- | :--- | :--- |
| `force` | `boolean` | Bypass non-blocking warnings (if authorized). |
| `justification` | `string` | Audit reason recorded when overriding non-blocking gates. |

---

## 4. Inline Comments & Code Suggestions

### `POST /cl/{id}/comments`

Creates a draft or published inline comment on a single line or multi-line range.

| Field (Form or JSON) | Type | Description |
| :--- | :--- | :--- |
| `file_path` | `string` | Target repository file path. |
| `line_number` | `int` | Starting line number (1-indexed). |
| `end_line_number` | `int` | Optional ending line number for multi-line block comments. |
| `side` | `string` | `base` (left pane) or `mod` (right pane). |
| `body` | `string` | Markdown comment text (supports ```` ```suggestion ```` blocks). |
| `is_draft` | `boolean` | `true` (default) to save as a draft; `false` to publish immediately. |
| `suggestion_code` | `string` | Optional raw replacement code for one-click application. |

### Comment Lifecycle Actions

| Method | Endpoint | Description |
| :--- | :--- | :--- |
| `POST` | `/cl/{id}/comments/{comment_id}/resolve` | Marks a comment thread resolved (`resolved=true`) or reopens it (`resolved=false`). |
| `POST` | `/cl/{id}/comments/{comment_id}/discard` | Deletes an unpublished draft comment. |
| `POST` | `/cl/{id}/comments/{comment_id}/apply-suggestion` | Splices the comment's code suggestion into the file tree, writes the new blob to CAS, and creates Patchset $N+1$. |
| `POST` | `/cl/{id}/comments/{comment_id}/reject-suggestion` | Marks the code suggestion rejected without modifying the changelist. |
| `POST` | `/cl/{id}/ai-hint/generate` | Invokes `[critique.ai]` on the selected `file`, `line`, `end_line`, and `side` to pre-fill a draft comment and code suggestion. |

---

## 5. CI Check Runs API (`/api/v1/ci/status`)

CI runners and `niebla ci report` use `/api/v1/ci/status` to report and query build, lint, and test statuses.

### `POST /api/v1/ci/status`

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

| JSON Field | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `cl_id` | `int` | Yes | Target changelist ID. |
| `patchset` | `int` | Yes | Target patchset number. |
| `runner_name` | `string` | Yes | Runner identifier matching `OWNERS.toml` / `ci.toml`. |
| `status` | `string` | Yes | `queued`, `running`, `passed`, `failed`, `canceled`, or `skipped`. |
| `provider` | `string` | No | Driver type (`local`, `forgejo`, `github`, `webhook`). |
| `url` | `string` | No | External CI build dashboard URL. |
| `log_hash` | `string` | No | 64-character hex SHA-256 digest of the build log stored in CAS. |

### `GET /api/v1/ci/status?cl_id={id}&patchset={n}`

Returns a JSON array of all check runs recorded for the specified changelist and patchset.
