---
title: Architectural Philosophy
description: Why Niebla uses the Google Piper and CitC model instead of distributed Git packfiles.
---

Niebla was engineered around the reality that enterprise monorepos eventually outgrow distributed version control systems like Git.

## The Git Dilemma at Monorepo Scale

Git was designed for Linux kernel development, where every developer clones the entire history of the repository onto their laptop. At monorepo scale (hundreds of gigabytes, millions of files, large datasets):

1. **Clone times become unbearable:** Downloading gigabytes of historical packfiles blocks developer onboarding.
2. **Disk bloat:** Multiple branches and workspaces duplicate massive trees on developer disks.
3. **The Git-LFS band-aid:** Large binary files require external HTTP servers and complex smudge/clean filters that frequently break.
4. **Merge skew & rebase hell:** Long-lived feature branches diverge rapidly from fast-moving trunk commits.

---

## The Niebla Model: Piper & CitC

Niebla replaces the distributed clone model with a **server-authoritative Merkle tree architecture**:

### 1. Instant Virtual Workspaces (NieblaFS)
When you run `niebla workspace create`, zero file contents are downloaded. The directory tree appears instantly via FUSE. File bytes are only streamed on-demand when your compiler or editor opens them, and are cached once in a shared local CAS directory (`~/.cache/niebla/blobs`).

### 2. Native Content-Addressable Storage (CAS)
Every file—whether a 10-line source file or a 4GB dataset—is represented as an immutable SHA-256 blob in CAS. There is no distinction between code and binaries. Git-LFS is obsolete by design.

### 3. Server-Authoritative Monotonic Trunk
Trunk revisions form a strict sequential sequence (`1, 2, 3...`). When parent changelists commit, Niebla's background cascade engine automatically rebases stacked child changelists in the cloud.

