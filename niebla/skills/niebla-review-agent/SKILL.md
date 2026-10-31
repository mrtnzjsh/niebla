---
name: niebla-review-agent
description: >-
  Workflows, APIs, adaptive context aperture rules, and dual suggestion formats for AI review agents
  and automated personas in Niebla and Cotejo. Use when generating AI code hints, posting inline review
  comments with actionable one-click code suggestions, applying suggested patches, or registering
  agents in .niebla/agents.toml, CAS, and OWNERS.toml.
---

# Niebla Review Agent & AI Comment Hints Guide

This skill guides AI agents and developers in authoring, invoking, and integrating **automated review personas** (accessibility, security, performance, architecture) and **AI-generated inline code suggestions** within Niebla and the Cotejo code review UI.

---

## 1. Core Architectural Principles & 3-Tier Governance

1. **3-Tier Enterprise Governance Model:**
   - **Tier 1 (Host/Admin Baseline — `~/.config/niebla/*.toml`):** Configures LLM providers (`[critique.ai]`), TLS/auth policies, and global submission baseline (`min_approvals`, `require_green_to_submit`).
   - **Tier 2 (Shared Repo Catalog — `.niebla/agents.toml`):** Defines reusable review agents (`type = "ai"`, `type = "cas_binary"`, `type = "in_tree"`, `type = "exec"`). Catalog entries can declare global `trigger_paths` or set `opt_in = true` so they only run when explicitly requested by a subtree's `OWNERS.toml`.
   - **Tier 3 (Hierarchical Subtree Policy — `OWNERS.toml`):** Binds named agents to a directory (`agents = ["a11y-bot", "sec-bot"]`) or to specific file globs under `[per_file]` (`"*.templ" = { approvers = ["@design-system"], agents = ["a11y-bot"] }`).
2. **Centralized Outbound Client (`pkg/httpclient` + `pkg/ai`):**
   - All outbound LLM and webhook requests use `pkg/httpclient`, sharing a single TLS 1.3 + `x509.CertPool` (`ca_file`, `server_name`) builder and `AuthConfig` (`bearer`, `token`, `basic`, `header`, `hmac`) `http.RoundTripper` across gRPC, CI providers, webhooks, and OpenAI-compatible endpoints.
3. **Adaptive Context Aperture (`pkg/ai.GenerateWithAperture`):**
   - Agents minimize token consumption while guaranteeing compiler-clean, syntactically valid code replacements by escalating context across 3 tiers:
     - **Tier 1 (`lines`):** Only the selected comment line range $[L_{\text{start}}, L_{\text{end}}]$.
     - **Tier 2 (`block`):** If the comment or snippet requires surrounding variable types or function signatures (`sufficient_context: false`), widens to the enclosing function/type block ($\pm 30$ lines).
     - **Tier 3 (`file`):** Widens to the entire file (bounded by `critique.ai.max_context_lines`, default `1200`).
4. **Dual Suggestion Storage (Markdown + Database Columns):**
   - Every code suggestion attached to a comment is persisted in **both**:
     - A standard Markdown ```` ```suggestion ```` fenced block inside `comment.body`.
     - Dedicated PostgreSQL columns on `cl_comments` (`has_suggestion`, `suggestion_start_line`, `suggestion_end_line`, `suggestion_code`, `suggestion_applied_ps`).
5. **One-Click Patch Application & Auto-Resolution:**
   - When the CL author accepts a suggestion (`POST /cl/{id}/comments/{comment_id}/apply-suggestion`), Cotejo splices `suggestion_code` into `[suggestion_start_line, suggestion_end_line]`, writes the new blob to CAS (`~/.cache/niebla/blobs`), recomputes the Merkle tree, creates **Patchset $N+1$**, and automatically resolves the comment thread (`resolved = true`, `suggestion_applied_ps = N+1`).

---

## 2. Step-by-Step Workflows for Creating & Registering Review Agents

### Workflow A: Create a Declarative AI Persona Agent (`type = "ai"`)
Zero Go code required. Uses `niebla-server`'s built-in `pkg/ai.Client` (`[critique.ai]`) and a Markdown system prompt in `.niebla/prompts/<domain>.md`.

1. **Author the System Prompt (`.niebla/prompts/<domain>.md`):**
   Instruct the LLM to analyze the touched files and return structured JSON matching `AgentReviewReport`:
   ```json
   {
     "verdict": "approved | changes_requested | commented",
     "summary": "1-2 sentence executive summary of findings",
     "comments": [
       {
         "file_path": "internal/web/views/cl.templ",
         "line_start": 42,
         "line_end": 44,
         "body": "Explain the accessibility/security/architecture issue clearly.",
         "suggestion_code": "exact replacement code for lines [line_start, line_end]"
       }
     ]
   }
   ```
   *(See `.niebla/prompts/accessibility.md` and `.niebla/prompts/security.md` for reference templates.)*

2. **Register the Persona in `.niebla/agents.toml`:**
   ```toml
   [[agent]]
   name = "a11y-bot"
   display_name = "Accessibility (WCAG 2.1 AA) Review Agent"
   type = "ai"
   prompt_file = ".niebla/prompts/accessibility.md"
   trigger_paths = ["internal/web/**/*.templ", "internal/web/**/*.html", "internal/web/**/*.css"]
   blocking = false
   auto_approve = true
   capture_logs = true
   timeout_seconds = 60
   ```

### Workflow B: Precompile & Register a Hermetic CAS Binary Agent (`type = "cas_binary"`)
Ideal for deterministic static analysis or standalone review binaries (`tools/agents/review-agent`) pinned by SHA-256 in Content-Addressable Storage (`~/.cache/niebla/blobs`).

1. **Build the Universal Review Agent (or Custom Go Agent):**
   ```bash
   go -C niebla build -o /tmp/review-agent ./tools/agents/review-agent
   ```
2. **Upload Binary to CAS & Record Digest:**
   ```bash
   niebla cas put /tmp/review-agent
   # Prints SHA-256 digest, e.g., e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
   ```
3. **Register in `.niebla/agents.toml`:**
   ```toml
   [[agent]]
   name = "sec-cas-bot"
   display_name = "Hermetic Security Auditor (CAS)"
   type = "cas_binary"
   cas_sha256 = "<sha256-from-niebla-cas-put>"
   args = ["--domain", "security", "--prompt", ".niebla/prompts/security.md"]
   opt_in = true
   blocking = true
   auto_approve = true
   capture_logs = true
   timeout_seconds = 60
   ```

### Workflow C: Author an In-Tree Script or Go Agent (`type = "in_tree"`)
Runs directly from the materialized CL workspace via `go run <entrypoint>` (for `.go` files) or direct execution.

1. **Environment Variables Injected by `internal/agents.Executor`:**
   - `NIEBLA_CL_ID`: Changelist ID (`int64`).
   - `NIEBLA_PATCHSET`: Patchset number (`int32`).
   - `NIEBLA_AGENT_NAME`: Agent identifier (`string`).
   - `NIEBLA_TOUCHED_FILES`: Newline-separated list of file paths modified in the CL.
2. **Supported Output Formats (`stdout`):**
   - **Structured JSON (`AgentReviewReport`):** Supports multi-line comments, custom summary, explicit `verdict`, and one-click `suggestion_code` patches.
   - **Standard Compiler Diagnostics (`file:line:col: message`):** Automatically parsed line-by-line into inline comments on `file` at `line`.
3. **Run `tools/agents/review-agent` Locally:**
   ```bash
   go run ./tools/agents/review-agent \
     --domain accessibility \
     --prompt .niebla/prompts/accessibility.md \
     --files "internal/web/views/cl.templ"
   ```

### Workflow D: Bind Agents to Subtrees or File Globs in `OWNERS.toml`
Subtree owners can attach catalog agents (including `opt_in = true` agents) to their directory or specific file patterns without editing `.niebla/agents.toml`:

```toml
inherited = true
approvers = ["@payments-core"]
required_approvals = 2
require_green_ci = true
allow_unresolved_comments = false

# Run these catalog agents on every CL touching this subtree:
agents = ["sec-bot", "pci-audit-agent"]
presubmit_hooks = ["sql-migration-lint"]
ci_runners = ["local", "fips-compliance-runner"]

# Or bind agents to specific file globs within the subtree:
[per_file]
"*.templ" = { approvers = ["@design-system"], agents = ["a11y-bot"] }
"*.sql" = { approvers = ["@dba-team"], presubmit_hooks = ["sql-safety-check"], required_approvals = 2 }
```

---

## 3. Programmatic Commenting & Suggestion API for External AI Agents

External or webhook-triggered AI agents can also post inline comments with actionable one-click code suggestions directly via Cotejo's HTTP endpoints:

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

### Step 2: Publish Review Drafts & Cast Verdict
```bash
curl -s -X POST "http://127.0.0.1:7331/cl/1/reply" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "author=agent/sec-bot" \
  --data-urlencode "verdict=changes_requested" \
  --data-urlencode "message=Automated security review complete: 1 suggested fix attached."
```

### Step 3: Interactive Hint Generation Endpoint (`ai-hint-agent`)
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
