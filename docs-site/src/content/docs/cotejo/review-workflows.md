---
title: Review Lifecycle & Diffs
description: Navigating intra-line word diffs, comparing patchsets, drafting multi-line comments, voting, and using keyboard shortcuts in Cotejo.
sidebar:
  order: 2
  label: Review Lifecycle & Diffs
---

This guide covers how to inspect diffs, compare patchset revisions, publish batched draft comments, and approve changelists in Cotejo.

---

## 1. Two-Pass Intra-Line Diffs

When viewing a changelist (`/cl/{id}`), you can switch between **Side-by-Side** and **Unified** diff layouts.

Rather than highlighting an entire modified line as a single block, Cotejo runs a two-pass diff engine:
1. **Line-Level Diff:** Identifies added, deleted, and modified line blocks using [Longest Common Subsequence (LCS)](https://en.wikipedia.org/wiki/Longest_common_subsequence).
2. **Intra-Line Word Slicing:** Within modified lines, isolates the exact tokens or characters that changed while preserving language syntax highlighting (`alecthomas/chroma/v2`).

---

## 2. Comparing Patchsets (Inter-Diffs)

Every `niebla shelve` creates a numbered patchset. Use the **Comparing** selector at the top of any changelist to choose which two snapshots to diff:

```text
Comparing: [ Patchset 1 ▼ ]  to  [ Patchset 2 ▼ ]
```

* **Trunk vs. Latest Patchset:** Shows the full cumulative change against the base trunk commit.
* **Patchset $N-1$ vs. Patchset $N$ (Inter-Diff):** Shows **only** what changed between those two patchsets. If an author changed 3 lines in 1 file out of a 30-file CL to address your feedback, the inter-diff displays only those 3 lines.

---

## 3. Draft Comments & Multi-Line Selection

### Batched Draft Comments
To avoid sending a notification for every individual comment as you read through a changelist:
1. New comments save as private **Drafts** (`is_draft = true`) visible only to you.
2. You can edit or discard drafts across any number of files while reviewing.
3. Click **Publish Comments** in the top bar to publish all drafts at once, record your review verdict, and update the Attention Set.

### Multi-Line Range Comments
To anchor a comment to a block of code rather than a single line:
1. Click and drag across the line numbers from the start line to the end line.
2. Write your comment (and optional ```` ```suggestion ```` block) in the form that opens below the selection.

### Resolving Threads
Every comment thread tracks an **Open** or **Resolved** state. When `allow_unresolved_comments = false` is set in `OWNERS.toml` (or `allow_submit_with_unresolved_comments = false` in `config.toml`), `niebla submit` is blocked until all threads are marked resolved.

---

## 4. Review Verdicts & Governance Sidebar

When publishing your review, select a verdict:

| Verdict | Meaning | Effect on Submission |
| :--- | :--- | :--- |
| **`LGTM`** | Approved | Counts toward `required_approvals` and subtree `OWNERS.toml` sign-off. |
| **`Changes Requested`** | Blocking feedback | Blocks `niebla submit` and moves the author into the Attention Set. |
| **`Commented`** | Non-blocking note | Publishes comments without approving or blocking. |

The **Governance Panel** in the right sidebar evaluates `OWNERS.toml` rules, unresolved thread counts, and CI check runs in real time to indicate whether the changelist is ready to submit.

---

## 5. Keyboard Shortcuts

| Shortcut | Action | Description |
| :--- | :--- | :--- |
| `j` / `k` | Next / Previous File | Scroll to the next or previous file in the changelist. |
| `n` / `p` | Next / Previous Comment | Jump to the next or previous comment thread. |
| `m` | Toggle Reviewed | Mark or unmark the focused file as `reviewed`. |
| `r` | Reply | Open a reply box on the focused comment thread. |
| `e` | Expand / Collapse All | Toggle all file diff panels on the page. |
| `?` | Shortcut Help | Open the keyboard shortcut reference modal. |
| `Esc` | Close | Close open comment forms or modals. |
