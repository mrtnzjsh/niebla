---
title: Overview & Philosophy
description: Why Niebla uses a server-authoritative monorepo model instead of distributed Git clones, and our open-source privacy guarantee.
sidebar:
  order: 1
  label: Overview & Philosophy
---

**Niebla** (*pronounced* **NYEH-blah**, [IPA](https://en.wikipedia.org/wiki/Help:IPA/Spanish): `[ˈnje.βla]`) is an open-source, server-authoritative [monorepo](https://en.wikipedia.org/wiki/Monorepo) version control and code review platform. It is designed to scale from a single developer working on a laptop to engineering teams running distributed CI pipelines and automated review tools.

:::tip[What does "Niebla" mean?]
***Niebla*** is the Spanish word for **"fog"** or **"mist."** The name is a play on bringing *cloud*-scale repository storage down to ground level, and reflects how virtual workspaces behave on your machine: like mist, the entire monorepo is visible around you without taking up weight on disk—materializing into solid files only where you reach out and touch them.
:::

---

## Why Move Beyond Distributed Git Clones?

Git was designed for distributed workflows where every machine downloads the full history of the repository. As a monorepo grows to hundreds of gigabytes or millions of files, that model creates friction:

1. **Slow clones and fetches:** Downloading gigabytes of historical packfiles slows down onboarding and CI jobs.
2. **Duplicated disk usage:** Every local branch or worktree duplicates the working tree on disk.
3. **Separate binary storage:** Large assets require external `git-lfs` servers and smudge/clean filters that break easily.
4. **Slow status checks:** Running `git status` must scan the entire working tree to find modified files.
5. **Noisy pull requests:** Force-pushes, fixup commits, and merge commits make it hard for reviewers to see what changed between review rounds.

---

## How Niebla Works

Niebla treats the monorepo as a central service backed by PostgreSQL and [Content-Addressable Storage (CAS)](https://en.wikipedia.org/wiki/Content-addressable_storage):

### 1. Virtual Workspaces (`nieblafs` & `--no-fuse`)
When you run `niebla workspace create`, no repository history is downloaded.
* **[FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) Mode (Linux default):** `nieblafs` mounts a virtual directory tree in milliseconds. File contents are streamed over [gRPC](https://en.wikipedia.org/wiki/GRPC) only when opened and cached once in `~/.cache/niebla/blobs`.
* **Direct Materialization (`--no-fuse`):** On macOS or inside unprivileged containers without `/dev/fuse`, Niebla materializes files directly to disk using [hardlinks](https://en.wikipedia.org/wiki/Hard_link) from the shared local CAS cache.

See [Workspaces & Storage](/niebla/workflows/workspaces/) and [Direct Materialization (`--no-fuse`)](/niebla/workflows/direct-materialization/) for details.

### 2. Fast Change Detection
Edits inside a workspace are written to a private [Copy-on-Write (CoW)](https://en.wikipedia.org/wiki/Copy-on-write) scratch layer. Because unmodified files are never copied into scratch, `niebla status` and `niebla diff` only inspect the files you actually touched.

### 3. Changelists & Patchsets over Branch Soup
Instead of pushing commits to feature branches, you run `niebla shelve` to publish a **Changelist (CL)**. Each update to a CL creates an immutable **Patchset** (`Patchset 1`, `Patchset 2`, ...), allowing reviewers in **Cotejo** to diff any two patchsets and see only the latest changes.

### 4. Linear Trunk & Automated Rebasing
When a changelist is approved and passes required checks, `niebla submit` commits it to [trunk](https://en.wikipedia.org/wiki/Trunk-based_development) as the next sequential revision (`1, 2, 3...`). If other changelists are stacked on top of that CL, `niebla-server` automatically rebases them in the background.

---

## Open Source & Zero-Telemetry Guarantee

Niebla is 100% open-source software released under the **MIT License**. There is no proprietary "enterprise core," hidden feature gating, or obfuscated code.

:::note[No Phoning Home or External Metrics]
**No part of Niebla ever phones home or collects telemetry for external use.**
* None of the binaries (`niebla`, `nieblafs`, `niebla-server`, `cotejo`) contain analytics trackers, crash reporters, license checks, or background update pings.
* All network communication stays strictly between your `niebla` client and your configured `niebla-server`.
* External network requests occur **only** if you explicitly configure outbound webhooks (`[events]`), CI dispatchers (`[ci]`), S3/SFTP backups (`[resilience]`), or an LLM provider (`[critique.ai]`). Even AI code hints can run entirely offline on your own hardware using local Ollama or vLLM models.
:::

---

## Where to Go Next

* **[Installation & Setup](/niebla/getting-started/installation/):** Install the `0.1.0` binaries on Linux, macOS, or NixOS.
* **[Quickstart Walkthrough](/niebla/getting-started/quickstart/):** Create a workspace, configure `OWNERS.toml`, review a changelist in Cotejo, and submit to trunk.
* **[System Architecture](/niebla/architecture/tech-stack/):** Explore how the four core binaries interact.
