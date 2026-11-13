---
title: niebla-server
description: Architecture, gRPC APIs, and storage tier of the central repository daemon.
---

`niebla-server` is the central authoritative gRPC service governing repository state, Merkle tree manifests, and changelist submissions.

## Architecture

- **gRPC Service (`port 50051`):** Exposes `NieblaService` implementing streaming blob uploads, tree node lookups, and changelist lifecycle RPCs.
- **PostgreSQL Database:** Tracks changelists, patchsets, file trees, check runs, and review comments.
- **Content-Addressable Storage (CAS):** Manages immutable, SHA-256 addressed blobs partitioned into sharded directory keys (`ab/cd/<sha256>`).
- **Cascade Rebasing Engine:** Automatically walks stacked changelist dependencies and computes three-way tree merges in the background when parent CLs land on trunk.
