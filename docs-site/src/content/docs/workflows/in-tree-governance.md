---
title: In-Tree Governance (OWNERS.toml)
description: Configure directory code ownership, approval quotas, automated review agents, and presubmit hooks directly in the repository tree.
sidebar:
  order: 5
  label: In-Tree Governance (OWNERS.toml)
---

In Niebla, repository policies—including code ownership, required approvals, automated review agents, and presubmit hooks—are versioned directly in the repository under `.niebla/` and per-directory `OWNERS.toml` files rather than external web settings.

Because these files live inside the [Merkle tree](https://en.wikipedia.org/wiki/Merkle_tree):
* **First-Class Version-Controlled Directory:** The `.niebla/` directory is tracked by default. Edits to `.niebla/OWNERS.toml`, `.niebla/agents.toml`, `.niebla/hooks.toml`, and `.niebla/prompts/*.md` appear in `niebla status` as `[A]` (added) or `[M]` (modified) and are shelved into changelists like any application code.
* **Changes to policy are reviewed like code:** Updating an owner rule, linter script, or AI prompt goes through a standard Changelist (CL) in Cotejo.
* **No surprise breakages on open CLs:** Every changelist is evaluated against the rules present at its base CL revision.
* **Selective Ignore:** If developers maintain local, untracked development settings inside `.niebla/`, they can selectively ignore them using `.nieblaignore` (for example, `.niebla/local.json`). Only runtime daemon socket files (`nieblafs.sock`, `*.sock`) are excluded automatically.

---

## 1. Repository Governance Layout

```text
monorepo/
├── .niebla/
│   ├── OWNERS.toml           # Root fallback owners and global approval defaults
│   ├── agents.toml           # Catalog of review scripts, linters, and AI personas
│   ├── hooks.toml            # Client-side and server-side presubmit hooks
│   └── prompts/              # Markdown prompts for AI review personas
│       ├── accessibility.md
│       └── security.md
├── tools/
│   └── agents/               # In-tree review scripts
│       └── sec-scanner.sh
└── services/
    └── auth/
        ├── OWNERS.toml       # Subtree approvers, quotas, and required checks
        └── handlers.go
```

---

## 2. Subtree Ownership & Submit Gates (`OWNERS.toml`)

Teams place `OWNERS.toml` files in their directories to control who can approve changes and which checks must pass before `niebla submit` succeeds:

```toml
# services/auth/OWNERS.toml

# 1. Authorized human approvers (at least one must approve files in this directory)
approvers = ["alice", "bob", "@security-leads"]

# 2. Reviewers automatically added to the attention set on 'niebla shelve'
reviewers = ["carol"]

# 3. Minimum number of human approvals required
required_approvals = 2

# 4. Automated review agents from .niebla/agents.toml to run on shelve
agents = ["sec-scanner", "a11y-auditor"]

# 5. Server-side presubmit hooks from .niebla/hooks.toml required for this subtree
presubmit_hooks = ["no-raw-sql"]

# 6. Require passing CI check runs before submitting
require_green_ci = true
ci_runners = ["auth-service-unit-tests"]

# 7. Block submission while any Cotejo comment thread is unresolved
allow_unresolved_comments = false

# 8. Stop inheriting parent/root directory approvers
set_noparent = true

# 9. File-pattern overrides within this directory
[per_file]
"*.proto" = { approvers = ["@proto-team"], agents = ["proto-linter"] }
"*.sql"   = { approvers = ["@dba-team"], presubmit_hooks = ["sql-migration-check"] }
```

### Multi-Directory Evaluation Rules
When a changelist modifies files across multiple directories, `niebla-server` walks up the directory tree from each modified file to the root:
* **`approvers`:** Each touched directory must have at least one approval from an authorized approver in its scope.
* **`required_approvals`:** Uses the **highest** approval count across all touched `OWNERS.toml` files.
* **`agents` & `ci_runners`:** Runs the **union** of all required agents and CI runners across touched directories.
* **`require_green_ci`:** Enforced if **any** touched `OWNERS.toml` sets `require_green_ci = true`.
* **`set_noparent = true`:** Prevents parent directory owners from satisfying approvals for sensitive subdirectories (such as `pkg/crypto/`).

---

## 3. Automated Review Agents (`.niebla/agents.toml`)

When you run `niebla shelve`, the server matches modified file paths against `.niebla/agents.toml` and `OWNERS.toml`, runs matching agents in an isolated workspace, and posts their findings as inline review comments in Cotejo.

Niebla supports four agent execution types:

### 1. `type = "in_tree"` — Repository Scripts
Runs a shell, Python, or Go script stored inside the repository. The script receives `NIEBLA_CL_ID`, `NIEBLA_PATCHSET`, `NIEBLA_CHANGED_FILES`, and `NIEBLA_BASE_CL`, and can emit `path:line: message` diagnostics on stdout:

```bash
#!/usr/bin/env bash
# tools/agents/sec-scanner.sh
set -euo pipefail
FAILED=0

for file in $NIEBLA_CHANGED_FILES; do
    if [[ "$file" =~ \.go$ ]] && [ -f "$file" ]; then
        if grep -n -E 'const [a-zA-Z0-9_]*Secret = "[^"]+"' "$file"; then
            line_no=$(grep -n -E 'const [a-zA-Z0-9_]*Secret = "[^"]+"' "$file" | head -n1 | cut -d: -f1)
            echo "$file:$line_no: error: hardcoded secret detected; use environment variables"
            FAILED=1
        fi
    fi
done

exit $FAILED
```

Register the script in `.niebla/agents.toml`:

```toml
[[agent]]
name = "sec-scanner"
display_name = "Security Pattern Scanner"
type = "in_tree"
entrypoint = "tools/agents/sec-scanner.sh"
trigger_paths = ["pkg/auth/**", "services/**/auth/**"]
auto_approve = true            # Grants LGTM if the script exits 0
capture_logs = true            # Stores stdout/stderr in CAS for viewing in Cotejo
post_inline_diagnostics = true # Converts path:line output into inline diff comments
timeout_seconds = 60
```

### 2. `type = "ai"` — Prompt-Driven AI Review Personas
Evaluates diffs using the LLM endpoint configured in `[critique.ai]` (such as a local Ollama/vLLM server or OpenAI-compatible API) and an in-tree Markdown prompt:

```toml
[[agent]]
name = "a11y-auditor"
display_name = "WCAG 2.1 Accessibility Auditor"
type = "ai"
prompt_file = ".niebla/prompts/accessibility.md"
trigger_paths = ["**/*.templ", "**/*.html", "**/*.tsx"]
auto_approve = true
post_inline_diagnostics = true
timeout_seconds = 45
```

See [AI Hints & Suggestions](/niebla/cotejo/ai-assistance/) for the JSON response schema and one-click patch application in Cotejo.

### 3. `type = "cas_binary"` — Hermetic Content-Addressed Review Binaries
For compiled static analyzers that shouldn't bloat the source tree, upload the compiled binary to [CAS](https://en.wikipedia.org/wiki/Content-addressable_storage) (`niebla cas put ./bin/linter`) and reference it by its [SHA-256](https://en.wikipedia.org/wiki/SHA-2) hash:

```toml
[[agent]]
name = "hermetic-linter"
display_name = "Hermetic Monorepo Linter"
type = "cas_binary"
cas_sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
args = ["--strict", "--max-complexity=15"]
trigger_paths = ["pkg/**", "services/**"]
auto_approve = true
post_inline_diagnostics = true
timeout_seconds = 120
```

### 4. `type = "exec"` — Host System Binaries
Invokes an executable installed on the server host `$PATH` (such as `shellcheck` or `clang-tidy`):

```toml
[[agent]]
name = "shellcheck-bot"
display_name = "ShellCheck Static Analysis"
type = "exec"
command = "/usr/bin/shellcheck"
args = ["-f", "gcc"]
trigger_paths = ["**/*.sh", "scripts/**"]
auto_approve = true
post_inline_diagnostics = true
```

---

## 4. Presubmit Hooks (`.niebla/hooks.toml`)

`.niebla/hooks.toml` defines checks that run either locally on the developer's machine before an RPC is sent (`[[client.presubmit]]`) or on `niebla-server` inside the submit lock (`[[server.presubmit]]`):

```toml
# .niebla/hooks.toml
version = 1

# Client-side hooks (can be bypassed locally with 'niebla submit --no-verify')
[[client.presubmit]]
name = "gofmt"
description = "Verify Go code formatting"
command = "gofmt -l ."
fail_on_output = true

# Server-side hooks (enforced during SubmitCL)
[[server.presubmit]]
name = "conventional-commits"
type = "metadata"
blocking = true
description = "Enforce Conventional Commits format on changelist descriptions"
[server.presubmit.metadata]
field = "description"
pattern = "^(feat|fix|docs|refactor|test|chore|perf)(\\(.+\\))?: .+"
min_length = 10

[[server.presubmit]]
name = "block-credentials"
type = "tree_scan"
blocking = true
description = "Block private keys and oversized files"
[server.presubmit.tree_scan]
forbidden_patterns = ["*.pem", "*.key", "id_rsa*", "*.pfx"]
max_file_size_bytes = 10485760 # 10 MB
```
