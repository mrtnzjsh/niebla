---
name: niebla-review-agent
description: >-
  Workflows, APIs, adaptive context aperture rules, and dual suggestion formats for AI review agents
  and automated personas in Niebla and Cotejo. Use when generating AI code hints, posting inline review
  comments with actionable one-click code suggestions, applying suggested patches, or registering
  agents in .niebla/agents.toml and CAS.
---

# Niebla Review Agent & AI Comment Hints Guide

This skill guides AI agents and developers in authoring, invoking, and integrating **automated review personas** and **AI-generated inline code suggestions** within Niebla and the Cotejo code review UI.

---

## 1. Core Architectural Principles

1. **Centralized Outbound Client (`pkg/httpclient` + `pkg/ai`):**
   - All outbound LLM and webhook requests use `pkg/httpclient`, sharing a single TLS 1.3 + `x509.CertPool` (`ca_file`, `server_name`) builder and `AuthConfig` (`bearer`, `token`, `basic`, `header`, `hmac`) `http.RoundTripper` across gRPC, CI providers, webhooks, and OpenAI-compatible endpoints.
2. **Adaptive Context Aperture (`pkg/ai.GenerateWithAperture`):**
   - Agents must minimize token consumption while guaranteeing compiler-clean, syntactically valid code replacements by escalating context across 3 tiers:
     - **Tier 1 (`lines`):** Only the selected comment line range $[L_{\text{start}}, L_{\text{end}}]$.
     - **Tier 2 (`block`):** If the comment or snippet requires surrounding variable types or function signatures (`sufficient_context: false`), widens to the enclosing function/type block ($\pm 30$ lines).
     - **Tier 3 (`file`):** Widens to the entire file (bounded by `critique.ai.max_context_lines`, default `1200`).
3. **Dual Suggestion Storage (Markdown + Database Columns):**
   - Every code suggestion attached to a comment is persisted in **both**:
     - A standard Markdown ```` ```suggestion ```` fenced block inside `comment.body`.
     - Dedicated PostgreSQL columns on `cl_comments` (`has_suggestion`, `suggestion_start_line`, `suggestion_end_line`, `suggestion_code`, `suggestion_applied_ps`).
4. **One-Click Patch Application & Auto-Resolution:**
   - When the CL author accepts a suggestion (`POST /cl/{id}/comments/{comment_id}/apply-suggestion`), Cotejo splices `suggestion_code` into `[suggestion_start_line, suggestion_end_line]`, writes the new blob to CAS (`~/.cache/niebla/blobs`), recomputes the Merkle tree, creates **Patchset $N+1$**, and automatically resolves the comment thread (`resolved = true`, `suggestion_applied_ps = N+1`).

---

## 2. Programmatic Commenting & Suggestion API for AI Agents

Autonomous AI agents reviewing a changelist can post inline comments with actionable one-click code suggestions directly via Cotejo's HTTP endpoints:

### Step 1: Post Draft Inline Comment with Suggestion
```bash
curl -s -X POST "http://127.0.0.1:7331/cl/1/comments" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "cl_id=1" \
  --data-urlencode "patchset_number=2" \
  --data-urlencode "file_path=pkg/auth/token.go" \
  --data-urlencode "line_number=10" \
  --data-urlencode "end_line_number=14" \
  --data-urlencode "side=modified" \
  --data-urlencode "author=agent/sec-bot" \
  --data-urlencode "body=Use hmac.Equal for constant-time signature verification." \
  --data-urlencode "has_suggestion=true" \
  --data-urlencode "suggestion_start_line=10" \
  --data-urlencode "suggestion_end_line=14" \
  --data-urlencode "suggestion_code=if !hmac.Equal([]byte(sig), expectedSig) {
	return ErrInvalidSignature
}"
```
*(Alternatively, embedding a ```` ```suggestion ... ``` ```` block inside `body` automatically populates the suggestion columns on insert.)*

### Step 2: Publish Review Drafts & Cast Verdict
```bash
curl -s -X POST "http://127.0.0.1:7331/cl/1/reply" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "author=agent/sec-bot" \
  --data-urlencode "verdict=changes_requested" \
  --data-urlencode "message=Automated security review complete: 1 suggested fix attached."
```

### Step 3: Interactive Hint Generation Endpoint
Human reviewers or UI tools can request an AI-generated suggestion for any line range before saving a comment:
```bash
curl -s -X POST "http://127.0.0.1:7331/cl/1/ai-hint/generate" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "cl_id=1" \
  --data-urlencode "patchset_number=2" \
  --data-urlencode "file_path=pkg/auth/token.go" \
  --data-urlencode "line_number=10" \
  --data-urlencode "end_line_number=14" \
  --data-urlencode "body=Replace string comparison with constant-time HMAC check"
```

---

## 3. Standalone CLI Execution (`ai-hint-agent`)

The `ai-hint-agent` binary (`tools/agents/ai-hint-agent/main.go`) can also be executed directly from the command line or inside CI pipelines:

```bash
go run ./tools/agents/ai-hint-agent \
  --file pkg/auth/token.go \
  --line-start 10 \
  --line-end 14 \
  --comment "Use constant-time comparison" \
  --prompt .niebla/prompts/comment_hint.md
```

Output JSON schema:
```json
{
  "sufficient_context": true,
  "explanation": "Replaces string equality check with constant-time comparison",
  "replacement_start_line": 10,
  "replacement_end_line": 14,
  "replacement_code": "if subtle.ConstantTimeCompare([]byte(a), []byte(b)) != 1 {\n\treturn ErrInvalidToken\n}",
  "aperture_used": "lines"
}
```

---

## 4. Registering Review Agents in `.niebla/agents.toml` & CAS

Review agents live in source control under `tools/agents/` and are declared in `.niebla/agents.toml`:

```toml
[[agent]]
name = "ai-hint-agent"
display_name = "AI Code Review Hint Assistant"
type = "in_tree"
entrypoint = "tools/agents/ai-hint-agent/main.go"
args = ["--prompt", ".niebla/prompts/comment_hint.md"]
blocking = false
auto_approve = false
capture_logs = true
timeout_seconds = 30
```

For distributed or hermetic execution without compiling source in the workspace, precompile the binary and store it in CAS (`~/.cache/niebla/blobs`):
1. Build the agent binary: `go build -o /tmp/ai-hint-agent ./tools/agents/ai-hint-agent`
2. Upload to CAS and record its SHA-256 digest in `.niebla/agents.toml` with `type = "cas_binary"` and `cas_sha256 = "<sha256>"`.

