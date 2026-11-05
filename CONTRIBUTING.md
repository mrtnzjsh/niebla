# Contributing to Niebla

Thank you for your interest in contributing to Niebla! 

---

## Repository Architecture & Upstream Mirroring

Niebla's primary development, automated code reviews, and containerized test runners are hosted on a self-hosted **Forgejo** instance. This GitHub repository serves as an official public mirror.

### How Contributions Work
You do **not** need a separate Forgejo account to contribute:
1. **Fork and Branch:** Fork this repository on GitHub and create a topic branch (`git checkout -b feature/my-feature`).
2. **Submit a Pull Request:** Open a standard Pull Request on GitHub.
3. **Automated Verification:** A maintainer will fetch your PR branch and run it through our comprehensive test suite (including containerized PostgreSQL and multi-agent concurrency tests) on Forgejo Actions.
4. **Upstream Merge:** Once approved, the maintainer merges your PR into `main` on Forgejo.
5. **Automatic GitHub Closure:** As soon as Forgejo mirrors back to GitHub, **GitHub automatically recognizes your commits and marks your PR as Merged** with full commit author attribution.

---

## Local Development Workflow

Niebla is written in Go and managed with Nix Flakes.

### Prerequisites
- Go 1.24+ (or Nix with flakes enabled: `nix develop`)
- Git

### Building the Project
From the repository root:
```bash
cd niebla
go build ./cmd/... ./tools/...
```

### Running Tests Locally
Run the unit test suite:
```bash
cd niebla
go test ./...
```
*(Heavy integration and containerized tests are isolated behind the `//go:build rc` tag and run in CI on release candidate builds without slowing down your local test loop).*

### Running Linters
```bash
cd niebla
go vet ./...
```

---

## Code Standards & Best Practices

To keep the codebase robust, readable, and maintainable, please adhere to these guidelines:

1. **No Magic Strings:** Use typed string constants rather than raw string literals for enum-like attributes (e.g. statuses, providers, error codes).
2. **Avoid Deep Nesting & Else Ladders:** Prefer early guard clauses (`if err != nil { return err }`) and `switch` statements over multi-branch `if / else if / else` chains.
3. **Domain Structs over Complex Tuples:** Functions should not return tuples of $\ge 4$ values. Package multi-value results into cohesive domain structs (e.g., `SubmitContext`, `CheckRunsSummary`, `BlobInfo`).
4. **Preserve Documentation:** Retain docstrings on exported functions, types, and constants.

---

## Reporting Issues & Questions

If you encounter bugs, regressions, or have questions about the architecture, please open an issue on GitHub. Maintainers actively triage and respond.

