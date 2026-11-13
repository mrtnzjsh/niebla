---
title: Cotejo Review Overview
description: Overview of the Google Critique-inspired web code review application.
---

**Cotejo** is Niebla's native web code review interface, inspired by Google Critique. It operates on port `8080` (or proxy port `7331` during local development).

## Features

- **Intra-line Word Diffing:** Sub-word change highlighting with syntax highlighting powered by Chroma.
- **Catppuccin Themes:** Preset themes including Catppuccin Mocha, Latte, and Frappe.
- **Threaded Inline Discussions:** Post draft comments, resolve discussions, or mark attention sets.
- **AI Suggestion Engine:** One-click automated code suggestions with direct Merkle tree patchset generation.
- **Strict Submit Gating:** Enforces OWNERS approval thresholds and green CI checks before allowing submissions to trunk.
