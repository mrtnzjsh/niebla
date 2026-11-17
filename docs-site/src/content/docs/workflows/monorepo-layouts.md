---
title: Monorepo Layouts & Patterns
description: Architectural blueprints and directory structures for organizing services, libraries, applications, and tools in a Niebla monorepo.
sidebar:
  order: 2
  label: Monorepo Layouts & Patterns
---

In a server-authoritative monorepo like Niebla, all codebases, shared libraries, and configuration live in a single unified [Merkle tree](https://en.wikipedia.org/wiki/Merkle_tree). 

Unlike distributed Git—where repository size causes clone bloat, slow fetches, and recursive submodule pain—Niebla uses zero-clone [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) mounts (`nieblafs`) and parallel [direct materialization](/niebla/workflows/direct-materialization/) (`--no-fuse`) so you can operate on any monorepo scale in under 15 milliseconds.

However, how you organize your directory tree directly determines:
* **Ownership Boundaries:** Where [`OWNERS.toml`](/niebla/workflows/in-tree-governance/) files define required reviewer sign-offs and CI runner gates.
* **Dependency Flow:** Preventing circular dependencies between business logic and shared infrastructure.
* **Review Scopes:** Ensuring [Cotejo](/niebla/cotejo/overview/) patchsets and automated AI review agents target the right teams.
* **IDE Performance:** Keeping language servers and indexers fast by opening scoped subtrees.

Below are the three standard monorepo layouts engineered for Niebla, followed by the repository-level control layer.

---

## 1. The In-Tree Control Layer: `.niebla/`

Every Niebla monorepo reserves `.niebla/` at the repository root. This directory contains version-controlled metadata, automated bot catalogs, and presubmit hooks:

```text
monorepo/
├── .niebla/                      # Version-controlled toolchain configuration
│   ├── OWNERS.toml               # Root fallback owners & repository-wide sign-off defaults
│   ├── agents.toml               # AI code review personas & automated linter bots
│   ├── hooks.toml                # Client-side and server-side presubmit hooks
│   ├── prompts/                  # Markdown system prompts for Cotejo review agents
│   │   ├── accessibility.md
│   │   └── security.md
│   └── ci/                       # Optional subtree CI workflow routing
│       └── workflows.yaml
├── .nieblaignore                 # Root build artifact & temporary file ignore patterns
└── ...                           # Product & platform source code
```

* **Root `OWNERS.toml`:** Serves as the fallback approval policy for root-level changes (e.g. `flake.nix`, root `.nieblaignore`, or shared build configs).
* **`agents.toml`:** Maps AI personas and security scanners to directory paths. When an author shelves a changelist, Niebla automatically dispatches bots based on modified files.
* **`hooks.toml`:** Enforces commit message formats (Conventional Commits), secret scanning, and max file sizes prior to shelving.

---

## 2. Pattern A: Services & Packages (Google / Systems Monorepo)

**Best for:** Go, Rust, C++, microservices, backend platforms, and systems engineering where high throughput, strict interfaces, and independent service deployability are required.

```text
monorepo/
├── .niebla/                      # Governance, bot catalog, presubmit gates
│   ├── OWNERS.toml
│   ├── agents.toml
│   └── hooks.toml
├── .nieblaignore                 # Excludes /target, /bin, *.o, etc.
│
├── proto/                        # Authoritative gRPC & Protobuf schemas
│   ├── auth/v1/
│   │   ├── OWNERS.toml           # Owned by Security & Identity squad
│   │   └── auth.proto
│   └── billing/v1/
│       ├── OWNERS.toml           # Owned by Core Platform squad
│       └── billing.proto
│
├── services/                     # Independent deployable binaries / microservices
│   ├── auth-service/
│   │   ├── OWNERS.toml           # Auth service engineering team
│   │   ├── cmd/main.go
│   │   ├── internal/
│   │   └── Dockerfile
│   ├── billing-service/
│   │   ├── OWNERS.toml           # Billing service engineering team
│   │   ├── cmd/main.go
│   │   ├── internal/
│   │   └── Dockerfile
│   └── gateway/
│       ├── OWNERS.toml
│       ├── cmd/main.go
│       └── internal/
│
├── pkg/                          # Shared reusable internal libraries
│   ├── cas/                      # Content-addressable storage client
│   │   └── OWNERS.toml           # Infra team review required
│   ├── crypto/                   # Cryptographic signing & hashing utilities
│   │   └── OWNERS.toml           # Security team review required
│   ├── db/                       # Database pooling & transaction helpers
│   ├── logging/                  # Structured CloudEvents & JSON logger
│   └── telemetry/                # OpenTelemetry metrics & distributed tracing
│
├── tools/                        # Internal developer tools, scripts & generators
│   ├── codegen/                  # Protobuf & OpenAPI code generation harnesses
│   ├── linters/                  # Custom AST linters & static analysis rules
│   └── scripts/                  # Cluster deploy & bootstrap scripts
│
└── third_party/                  # Vendored dependencies, schemas, or C libraries
```

### Architectural Rules
1. **Unidirectional Dependency Flow:**
   $$\text{services} \longrightarrow \text{pkg} \longrightarrow \text{proto}$$
   * `services/` may import from `pkg/` and `proto/`.
   * `pkg/` may import from `proto/` and other `pkg/` modules, but **never** from `services/`.
   * `proto/` is strictly standalone schema definitions with zero internal library dependencies.
2. **Schema-First Evolution:** Changes to API contracts (`proto/auth/v1/auth.proto`) are shelved in the same changelist as implementation updates across multiple dependent services, ensuring atomic compatibility on trunk.

---

## 3. Pattern B: Applications & Libraries (Web / Full-Stack Monorepo)

**Best for:** TypeScript, JavaScript, React/Next.js, Python, full-stack web applications, mobile apps, and shared design systems.

```text
monorepo/
├── .niebla/                      # Governance & presubmit checks
│   ├── OWNERS.toml
│   └── hooks.toml
├── .nieblaignore                 # Excludes node_modules/, dist/, .turbo/, etc.
│
├── apps/                         # User-facing applications and deployable targets
│   ├── web-storefront/           # Customer-facing Next.js application
│   │   ├── OWNERS.toml           # Frontend web squad
│   │   ├── src/
│   │   └── package.json
│   ├── mobile-app/               # React Native / Flutter iOS & Android client
│   │   ├── OWNERS.toml           # Mobile engineering team
│   │   └── src/
│   ├── admin-portal/             # Internal operations dashboard
│   │   ├── OWNERS.toml           # Internal tools squad
│   │   └── src/
│   └── docs-site/                # Developer documentation (Astro / Starlight)
│       └── src/
│
├── libs/                         # Shared libraries, SDKs, and design tokens
│   ├── ui-components/            # Shared component library (buttons, modals, tables)
│   │   ├── OWNERS.toml           # Design System & UX guild
│   │   └── src/
│   ├── auth-client/              # Session handling, OAuth tokens, and login widgets
│   │   ├── OWNERS.toml           # Identity & Security squad
│   │   └── src/
│   ├── analytics/                # Event streaming & client telemetry SDK
│   └── api-client/               # Type-safe RPC / REST SDK auto-generated from backend
│
└── tools/                        # Shared toolchain configurations
    ├── eslint-config/            # Monorepo-wide linting standards
    ├── tailwind-preset/          # Shared branding colors, fonts, and spacing
    └── tsconfig/                 # Base TypeScript compiler presets
```

### Architectural Rules
1. **Design System Governance:** Changes affecting `libs/ui-components/` require sign-off from the Design System guild (`libs/ui-components/OWNERS.toml`), preventing rogue visual drifts across `apps/web-storefront` and `apps/admin-portal`.
2. **Atomic Upgrades:** Updating a shared UI component and its consumers across `apps/web-storefront` and `apps/admin-portal` happens in a single Changelist, reviewed in Cotejo with side-by-side diffs, and landed in one revision without package version pinning.

---

## 4. Pattern C: Product Domains & Vertical Slices

**Best for:** Medium-to-large organizations organized into cross-functional product squads (e.g., Spotify squad model) where teams own both the frontend and backend for their business domain.

```text
monorepo/
├── .niebla/                      # Toolchain config & global review bots
│   ├── OWNERS.toml
│   └── hooks.toml
├── .nieblaignore
│
├── domains/                      # Organized by business capability
│   ├── checkout/
│   │   ├── OWNERS.toml           # Checkout Squad owns all files in this domain
│   │   ├── services/             # Backend order processing & payment service
│   │   │   └── checkout-api/
│   │   └── ui/                   # Frontend checkout drawer & payment form
│   │       └── checkout-flow/
│   ├── inventory/
│   │   ├── OWNERS.toml           # Inventory Squad
│   │   ├── services/
│   │   │   └── stock-api/
│   │   └── ui/
│   │       └── warehouse-dashboard/
│   └── user-management/
│       ├── OWNERS.toml           # Accounts & Profile Squad
│       ├── services/
│       │   └── profile-api/
│       └── ui/
│           └── account-settings/
│
├── platform/                     # Shared cross-cutting foundation
│   ├── base-services/            # API gateway, service mesh, auth proxy
│   ├── database/                 # Shared migration runners & connection pools
│   └── design-system/            # Foundational CSS tokens and headless primitives
│
└── tools/                        # Monorepo build scripts and local dev environments
```

### Architectural Rules
1. **Vertical Ownership:** The `checkout` team has full autonomy over `domains/checkout/`, reviewing their own backend and frontend code simultaneously without waiting for an external frontend or backend guild.
2. **Platform Guardrails:** Shared infrastructure under `platform/` requires approvals from the Core Infrastructure guild (`platform/OWNERS.toml`).

---

## 5. Integrating Layouts with Niebla Capabilities

### A. Subtree Ownership (`OWNERS.toml`)
Niebla walks up the directory tree to find the nearest `OWNERS.toml`. When shelving a changelist spanning multiple directories, Niebla calculates the union of required approvers:

```text
Changelist #42 modifies:
  • services/auth/handlers.go       -> requires approver from services/auth/OWNERS.toml
  • pkg/crypto/keys.go              -> requires approver from pkg/crypto/OWNERS.toml
```
Both teams must cast an approval (`LGTM`) in Cotejo before `niebla submit` will succeed.

### B. Ignore Strategy (`.nieblaignore`)
Because Niebla checks `status` and `diff` in $O(\text{changed})$ time against the local scratch layer, keep build artifacts ignored so scratch remains tiny:

* **Place common rules in root `.nieblaignore`:**
  ```text
  # Dependencies
  node_modules/
  vendor/

  # Build Artifacts
  dist/
  build/
  target/
  bin/

  # Temporary & Cache files
  *.tmp
  .turbo/
  .next/
  __pycache__/
  ```
* **Nested `.nieblaignore` files:** Any subdirectory can contain its own `.nieblaignore` to exclude specialized test fixtures or local mock databases.

### C. IDE Performance & Scoped Workspaces
In a large monorepo with thousands of files, IDEs (GoLand, VS Code, IntelliJ) can slow down if they index the entire repository. Niebla makes it simple to scope your work:

1. **Create Scoped Workspace:**
   ```bash
   niebla workspace create auth-feature --path services/auth --path pkg/crypto
   ```
2. **Open Just the Subfolder:**
   ```bash
   code ~/niebla/workspaces/auth-feature/services/auth
   ```
   The IDE only indexes `services/auth`, while `niebla status` and `niebla shelve` continue to track your changes seamlessly against the monorepo trunk.

---

## 6. Pattern Comparison Matrix

| Dimension | Pattern A: Services & Packages | Pattern B: Apps & Libraries | Pattern C: Product Domains |
| :--- | :--- | :--- | :--- |
| **Primary Stack** | Go, Rust, C++, Microservices | TypeScript, React, Next.js, Python | Polyglot (Go + React + Python) |
| **Team Structure** | Functional (Backend, Security, Infra) | Frontend, Mobile, Web squads | Cross-functional Product Squads |
| **Boundary Placement** | Horizontal (`services/`, `pkg/`, `proto/`) | Functional (`apps/`, `libs/`) | Vertical (`domains/<capability>/`) |
| **`OWNERS.toml` Focus** | API & Library contract security | UI Design System consistency | End-to-end domain autonomy |
| **Recommended Ignore Strategy** | Ignore `/bin`, `/target`, generated protobufs | Ignore `node_modules/`, `dist/`, `.turbo/` | Root `.nieblaignore` + domain-specific mocks |
