---
name: niebla-ci
description: >-
  Workflows, runner configuration, workflow authoring, and status reporting for the Niebla CI
  orchestration system (niebla-ci). Use when configuring local, Forgejo, GitHub Actions, or webhook
  runners, authoring CI workflow YAML, binding subtree CI runners in OWNERS.toml, running niebla ci exec
  or report, or troubleshooting check runs.
---

# Niebla CI Runner Orchestration (`niebla-ci`)

This skill guides AI agents and developers in configuring, dispatching, executing, and reporting continuous integration builds and tests in Niebla monorepo environments.

---

## 1. Architecture & Core Principles

Niebla decouples VCS state and code review from build execution via **composition over tight coupling**:

1. **Zero-Coupling Contract:**
   `niebla-server` and `cotejo` do not hardcode GitHub, Forgejo, or shell execution logic. Instead, `niebla-server` emits standard CloudEvents (`niebla.cl.created`, `niebla.cl.rebased`) and exposes a provider-agnostic **Check Runs API** (`POST /api/v1/ci/status`).
2. **Stateless Tree Materialization (`niebla export`):**
   Because Niebla is a trunk-based Merkle VCS rather than Git, runners cannot `git clone`. Instead, any runner materializes an exact snapshot in milliseconds using `niebla export --cl <id> --patchset <n> --dest ./src`. Local or container runners mounting a shared CAS cache volume achieve zero-copy hardlinking (`os.Link`) without consuming network bandwidth.
3. **Execution Wrapper (`niebla ci exec`):**
   Any arbitrary build command (`go test ./...`, `npm test`, `cargo test`) can be wrapped with `niebla ci exec`. It automatically reports `running` at startup, captures stdout/stderr, saves the log blob to Content-Addressable Storage (CAS), and reports `passed` or `failed` back to Cotejo.
4. **Trunk Submit Gate (`require_green_to_submit` & `OWNERS.toml` `require_green_ci`):**
   When enabled globally in `~/.config/niebla/config.toml` (`require_green_to_submit = true`) or per-subtree in `OWNERS.toml` (`require_green_ci = true` / `ci_runners = [...]`), `niebla submit` and Cotejo block submissions to trunk unless all required check runs for the active patchset are green (`passed` or `skipped`). Emergency overrides can be executed with `niebla submit --force` or an override justification in the web UI.

---

## 2. Declarative Runner Configuration (`config.toml` / `ci.toml`)

Runners are declared in `~/.config/niebla/config.toml` (or modular `~/.config/niebla/ci.toml`) under the `[ci]` section. By default, Niebla uses the zero-infrastructure **`local`** runner.

```toml
[ci]
enabled = true
default_runner = "local"          # Default runner target
require_green_to_submit = true    # Block SubmitCL if checks are pending or failed
server_url = "http://127.0.0.1:7331" # Cotejo / Niebla Server base URL for callbacks

# 1. Default Local Sandbox Runner
[ci.runners.local]
provider = "local"
work_dir = "/tmp/niebla-ci"       # Isolated ephemeral checkout directory
command = ["go", "test", "./..."]
timeout_seconds = 300
concurrency = 4

# 2. Forgejo / Gitea Actions Runner
[ci.runners.forgejo-linux]
provider = "forgejo"
endpoint = "https://git.example.com/api/v1/repos/org/monorepo/actions/workflows/ci.yml/dispatches"
ref = "main"
[ci.runners.forgejo-linux.auth]
mode = "token"                    # "token" (Forgejo personal access token)
token_env = "FORGEJO_CI_TOKEN"

# 3. GitHub Actions Runner
[ci.runners.github-build]
provider = "github"
endpoint = "https://api.github.com/repos/org/monorepo/dispatches"
event_type = "niebla_presubmit"
[ci.runners.github-build.auth]
mode = "bearer"                   # "bearer" (GitHub fine-grained or PAT token)
token_env = "GITHUB_PAT_TOKEN"

# 4. Custom Generic Webhook / Kubernetes Tekton / Argo Trigger
[ci.runners.custom-k8s]
provider = "webhook"
endpoint = "https://ci.corp.internal/hooks/niebla"
[ci.runners.custom-k8s.auth]
mode = "hmac"
secret_env = "NIEBLA_WEBHOOK_SECRET" # Injects X-Critique-Signature-256 header
```

### Supported Authentication Modes

| Mode | Header Injected | Secret Resolution Keys |
| :--- | :--- | :--- |
| `bearer` | `Authorization: Bearer <token>` | `token`, `token_env`, `key_file` |
| `token` | `Authorization: token <token>` | `token`, `token_env`, `key_file` |
| `basic` | `Authorization: Basic <base64>` | `username`, `password`, `password_env` |
| `header` | `<header_name>: <token>` | `header_name`, `token`, `token_env` |
| `hmac` | `X-Critique-Signature-256: sha256=<hex>` | `secret`, `secret_env`, `header_name` |
| `none` | None | N/A |

---

## 3. Subtree CI Governance (`OWNERS.toml`)

Subtree owners can require green CI and bind specific named CI runners (`ci_runners = [...]`) to their directory or file globs in `OWNERS.toml`, even if global `require_green_to_submit` is disabled:

```toml
inherited = true
approvers = ["@payments-core"]

# Enforce green CI for any CL touching this subtree:
require_green_ci = true

# Require these specific CI runners to report "passed" (or "skipped") on the active patchset:
ci_runners = ["local", "fips-compliance-runner"]

# Per-file CI runner overrides:
[per_file]
"*.sql" = { approvers = ["@dba-team"], ci_runners = ["sql-migration-runner"] }
```

* **Multi-Subtree Union Rule:** If a CL modifies files across multiple subtrees, `niebla-server` and Cotejo compute the union of all `ci_runners` across touched subtrees and verify that every required runner has reported `passed` (or `skipped`) on the active patchset before allowing `SubmitCL`.

---

## 4. CLI Command Reference

### Wrap and Execute a Build Command (`niebla ci exec`)

```bash
niebla ci exec --cl <id> [--patchset <n>] --runner <name> [--server-url <url>] [--json] -- <command> [args...]
```

* **Behavior:**
  1. Posts `status = "running"` to `/api/v1/ci/status`.
  2. Executes `<command>` with environment variables:
     * `NIEBLA_CL_ID`: Target changelist ID.
     * `NIEBLA_PATCHSET`: Target patchset number.
     * `NIEBLA_SERVER_URL`: Server callback base URL.
  3. Tees stdout/stderr live to the terminal while capturing full build logs.
  4. Stores the build log into CAS and posts `status = "passed"` (exit code 0) or `status = "failed"` (non-zero exit).
  5. Exits with the command's original exit code.

### Manually Report Check Status (`niebla ci report`)

```bash
niebla ci report --cl <id> [--patchset <n>] --runner <name> --status <status> [--url <url>] [--log-file <path>] [--json]
```

* **Status Values:** `queued`, `running`, `passed`, `failed`, `canceled`, `skipped`.
* When `--log-file` is provided, uploads the log to CAS and references its SHA-256 in the check run record.

---

## 5. Authoring Remote CI Workflows

### Forgejo Actions Workflow (`.forgejo/workflows/ci.yaml`)

```yaml
name: Niebla Presubmit CI
on:
  workflow_dispatch:
    inputs:
      cl_id:
        description: 'Niebla Changelist ID'
        required: true
      patchset:
        description: 'Patchset Number'
        required: true

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - name: Install Niebla CLI
        run: |
          curl -sSL https://niebla.corp/install.sh | bash

      - name: Materialize CL Workspace
        env:
          NIEBLA_SERVER: niebla.corp:50051
        run: |
          niebla export --cl ${{ inputs.cl_id }} --patchset ${{ inputs.patchset }} --dest ./src

      - name: Execute Build & Test
        env:
          NIEBLA_SERVER_URL: https://cotejo.corp
        run: |
          cd ./src
          niebla ci exec --cl ${{ inputs.cl_id }} --patchset ${{ inputs.patchset }} --runner forgejo-linux -- go test -v ./...
```

### GitHub Actions Workflow (`.github/workflows/niebla-presubmit.yaml`)

```yaml
name: Niebla Presubmit Build
on:
  repository_dispatch:
    types: [niebla_presubmit]

jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - name: Export Merkle Tree Snapshot
        env:
          NIEBLA_SERVER: niebla.corp:50051
        run: |
          niebla export --cl ${{ github.event.client_payload.cl_id }} \
                        --patchset ${{ github.event.client_payload.patchset }} \
                        --dest ./workspace

      - name: Run Test Suite
        env:
          NIEBLA_SERVER_URL: https://cotejo.corp
        run: |
          cd ./workspace
          niebla ci exec --cl ${{ github.event.client_payload.cl_id }} \
                         --patchset ${{ github.event.client_payload.patchset }} \
                         --runner github-build \
                         -- npm test
```

---

## 6. Troubleshooting & FAQ

* **Why is `niebla submit` failing with `CI check runs not passing` or `required CI runner "<name>" has not reported a check run`?**
  Either your monorepo has `require_green_to_submit = true` configured or a touched subtree's `OWNERS.toml` specifies `require_green_ci = true` / `ci_runners = ["<name>"]`. Run `niebla ci exec --cl <id> --runner <name> -- <cmd>` (or wait for remote runners to report `passed`). If you need to push an emergency trunk fix immediately, use `niebla submit --force`.
* **How do I inspect check runs for a CL?**
  * In the Cotejo Web UI: Open `http://127.0.0.1:7331/cl/<id>` to view check run badges and log output links.
  * Via REST API: `curl http://127.0.0.1:7331/api/v1/ci/status?cl_id=<id>&patchset=<n>`.
