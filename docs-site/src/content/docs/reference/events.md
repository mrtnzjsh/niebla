---
title: "CloudEvents & Webhooks"
description: Specification of Niebla's CloudEvents v1.0 event bus, webhook configuration, JSON payload schemas, and HMAC-SHA256 signature verification.
sidebar:
  order: 6
---

When enabled in `config.toml`, `niebla-server` publishes asynchronous [CNCF CloudEvents v1.0](https://cloudevents.io/) notifications whenever changelists are created, shelved, rebased, submitted, or verified by CI check runs.

Events are strictly opt-in (`[events] enabled = false` by default) and are only delivered to endpoints or local scripts you configure.

---

## 1. Webhook Configuration

Configure `[events]` in `~/.config/niebla/config.toml` (or `events.toml`):

```toml
[events]
enabled = true
sink = "webhook" # "webhook", "exec", "log", or "noop"
filter = ["niebla.cl.*", "niebla.check_run.*"]
buffer_size = 256

[events.webhook]
endpoint = "https://ci.internal/api/v1/niebla-webhook"
secret_token_env = "NIEBLA_EVENT_SECRET"
timeout_ms = 3000
max_retries = 3
```

---

## 2. CloudEvents Envelope & HTTP Headers

Every webhook `POST` request sends a CloudEvents v1.0 JSON envelope:

```json
{
  "specversion": "1.0",
  "id": "f81d4fae-7dec-11d0-a765-00a0c91e6bf6",
  "source": "niebla-server",
  "type": "niebla.cl.submitted",
  "subject": "cl/104",
  "time": "2026-11-14T03:45:00Z",
  "datacontenttype": "application/json",
  "data": { ... }
}
```

| HTTP Header | Value Format | Description |
| :--- | :--- | :--- |
| `Content-Type` | `application/json` | JSON payload encoding. |
| `X-Niebla-Event` | `niebla.cl.submitted` | CloudEvents `type` string for fast routing. |
| `X-Niebla-Signature` | `sha256=<hex_hmac>` | [HMAC-SHA256](https://en.wikipedia.org/wiki/HMAC) signature computed over the raw request body using `secret_token_env`. |

---

## 3. Event Catalog & Payload Schemas

### `niebla.cl.created`

Emitted when a new changelist (`Patchset 1`) is first shelved.

```json
{
  "specversion": "1.0",
  "type": "niebla.cl.created",
  "source": "niebla-server",
  "subject": "cl/42",
  "data": {
    "cl_id": "42",
    "author": "alice",
    "description": "feat(auth): implement passkey authentication",
    "parent_cl_id": "38",
    "patchset_number": 1,
    "root_tree_hash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
    "reviewers": ["bob", "@security-team"]
  }
}
```

### `niebla.cl.shelved`

Emitted when an updated patchset ($N+1$) is uploaded to an existing changelist.

```json
{
  "specversion": "1.0",
  "type": "niebla.cl.shelved",
  "source": "niebla-server",
  "subject": "cl/42/ps/2",
  "data": {
    "cl_id": "42",
    "patchset_number": 2,
    "author": "alice",
    "root_tree_hash": "a1b2c3d4...",
    "files_changed_count": 4
  }
}
```

### `niebla.cl.submitted`

Emitted when a changelist is committed to the linear trunk.

```json
{
  "specversion": "1.0",
  "type": "niebla.cl.submitted",
  "source": "niebla-server",
  "subject": "cl/42",
  "data": {
    "cl_id": "42",
    "author": "alice",
    "trunk_revision": 105,
    "root_tree_hash": "a1b2c3d4..."
  }
}
```

### `niebla.cl.rebased`

Emitted when a stacked child changelist is automatically re-parented onto an updated parent CL or trunk revision.

```json
{
  "specversion": "1.0",
  "type": "niebla.cl.rebased",
  "source": "niebla-server",
  "subject": "cl/43/ps/3",
  "data": {
    "cl_id": "43",
    "parent_cl_id": "42",
    "patchset_number": 3,
    "merged_tree_hash": "f4e3d2c1..."
  }
}
```

### `niebla.check_run.completed`

Emitted when a CI check run or automated review agent finishes executing.

```json
{
  "specversion": "1.0",
  "type": "niebla.check_run.completed",
  "source": "niebla-server",
  "subject": "cl/42/check/192",
  "data": {
    "cl_id": 42,
    "patchset_number": 2,
    "runner_name": "security-sentinel",
    "provider": "in_tree",
    "status": "passed",
    "exit_code": 0,
    "log_blob_sha256": "4f8b2d18..."
  }
}
```

---

## 4. HMAC-SHA256 Signature Verification

Always verify `X-Niebla-Signature` using [constant-time comparison](https://en.wikipedia.org/wiki/Timing_attack) before processing webhook payloads.

### Go

```go
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func VerifySignature(secret string, body []byte, sigHeader string) bool {
	if !strings.HasPrefix(sigHeader, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)

	provided, err := hex.DecodeString(strings.TrimPrefix(sigHeader, "sha256="))
	if err != nil {
		return false
	}
	return hmac.Equal(expected, provided)
}
```

### Python

```python
import hashlib
import hmac

def verify_signature(secret: str, raw_body: bytes, sig_header: str) -> bool:
    if not sig_header.startswith("sha256="):
        return False
    expected = hmac.new(secret.encode(), raw_body, hashlib.sha256).hexdigest()
    provided = sig_header.split("=", 1)[1]
    return hmac.compare_digest(expected, provided)
```
