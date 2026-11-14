---
title: Quickstart (5-Minute Tour)
description: Create your first Niebla workspace, edit files, shelve a changelist, and review in Cotejo.
sidebar:
  order: 3
  label: Quickstart (5-Minute Tour)
---

Welcome to Niebla! This guide will walk you through setting up your first virtual workspace, modifying files, and submitting changes to trunk.

## 1. Prerequisites

Ensure you have the Niebla CLI tools and server running. If you are on NixOS, enable the service:

```nix
services.niebla.enable = true;
```

Verify connectivity to the repository server:

```bash
niebla --server 127.0.0.1:50051 status
```

---

## 2. Create a Virtual Workspace

In Niebla, you do not clone repository packfiles. Instead, you create a virtual workspace backed by **NieblaFS**:

```bash
niebla workspace create my-feature --mount-dir ~/workspaces/my-feature
```

This completes in milliseconds! Your terminal or IDE sees the entire monorepo immediately:

```bash
cd ~/workspaces/my-feature
ls -la
```

---

## 3. Make Changes & Inspect Status

Edit or create files normally in your editor:

```bash
echo 'fmt.Println("Hello Niebla!")' >> main.go
```

Check the workspace status:

```bash
niebla status
```

Niebla compares your local Copy-on-Write (CoW) scratch layer against the base trunk changelist and displays added, modified, or deleted files.

---

## 4. Shelve a Changelist (CL)

When you are ready to prepare a review:

```bash
niebla shelve -m "feat: add greeting message"
```

This uploads only your modified blobs into central Content-Addressable Storage (CAS), computes the new Merkle root tree, and assigns a changelist ID (e.g., `CL 42`).

---

## 5. Review in Cotejo

Open the **Cotejo** web interface in your browser:

* **Localhost:** `http://localhost:8080/cl/42`
* **Remote / Self-Hosted:** `http://<cotejo-host>:8080/cl/42` (or `https://cotejo.yourcompany.internal/cl/42`)

Here you can:
- View syntax-highlighted side-by-side diffs.
- Post inline review comments.
- Request AI assistance via the **AI Hint Agent**.
- Approve and submit the changelist to trunk!

---

## Next Steps

Ready for an end-to-end, code-complete walkthrough? Continue to:

* **[First Project: Hello World Walkthrough](file:///home/matatan/Developer/niebla/docs-site/src/content/docs/getting-started/first-project.md):** Build a Go microservice from scratch, create an `OWNERS.toml` governance policy, shelve multiple patchsets, resolve Cotejo comments, and submit to trunk.

