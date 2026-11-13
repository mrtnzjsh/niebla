---
title: "IDE Setup & Configuration"
description: Optimizing Visual Studio Code, Neovim, and Emacs for Niebla workspaces, disabling Git extensions, configuring file watchers, and setting up Language Server Protocols (LSP).
sidebar:
  order: 4
  label: IDE Setup (VS Code, Neovim, Emacs)
---

Because Niebla workspaces are mounted as standard POSIX directories, modern editors, language servers, and command-line tools function out of the box.

However, because Niebla is **server-authoritative and does not use Git**, IDE extensions that assume a local `.git` repository should be tuned. Without tuning, an editor might search for `.git/config` across parent directories, start full-depot file watchers, or attempt recursive inotify scans on virtual directories.

This guide provides tested, copy-paste configurations for **Visual Studio Code**, **Neovim**, and **Emacs**.

---

## Why Git Extensions Should Be Disabled

Most developer environments come with active Git integrations:
1. **Unnecessary Directory Searches:** Looking for `.git/HEAD` or `.git/index` can cause the editor to traverse parent directories up to the filesystem root.
2. **Inotify / File Watcher Saturation:** In multi-gigabyte monorepos, eager file watchers can exhaust system inotify limits (`fs.inotify.max_user_watches`) by indexing virtual directories that have not yet been touched.
3. **Misleading UI Indicators:** The editor might display a "Not a Git repository" warning or fail to show file status until configured.

---

## Visual Studio Code

### What to Expect
* Language servers (**gopls**, **rust-analyzer**, **tsserver**, **pyright**) resolve imports and dependencies normally through the virtual mount.
* The built-in Git extension will show a badge indicating "No source control providers registered," which is normal.

### Recommended `.vscode/settings.json`

Add the following to your workspace or user settings:

```json
{
  // Disable built-in Git extension to avoid scanning for .git
  "git.enabled": false,
  "git.autorefresh": false,

  // Prevent file watchers from recursing into scratch overlays or build caches
  "files.watcherExclude": {
    "**/.niebla/**": true,
    "**/scratch/**": true,
    "**/.git/**": true,
    "**/node_modules/**": true,
    "**/target/**": true,
    "**/dist/**": true
  },

  // Language Server Scoping (e.g. Go gopls)
  "gopls": {
    "ui.diagnostic.analyses": {
      "unusedparams": true
    },
    "formatting.gofumpt": true
  },

  // Rust Analyzer Scoping
  "rust-analyzer.check.command": "clippy"
}
```

---

## Neovim

### What to Expect
* Neovim navigates FUSE workspaces with zero latency.
* Built-in LSP clients and Treesitter parse virtual files effortlessly.

### Recommended Configuration (`init.lua`)

Configure your LSP client to detect project roots using project markers (such as `go.mod`, `Cargo.toml`, or `package.json`) rather than seeking a `.git` root:

```lua
-- nvim-lspconfig root directory detection without .git dependency
local lspconfig = require('lspconfig')
local util = require('lspconfig.util')

-- Configure gopls
lspconfig.gopls.setup({
  root_dir = util.root_pattern('go.work', 'go.mod', '.nieblaignore', '.niebla'),
  settings = {
    gopls = {
      analyses = { unusedparams = true },
      staticcheck = true,
    },
  },
})

-- Configure rust-analyzer
lspconfig.rust_analyzer.setup({
  root_dir = util.root_pattern('Cargo.toml', '.nieblaignore', '.niebla'),
})

-- Telescope: Use ripgrep with .nieblaignore instead of git_files
local builtin = require('telescope.builtin')
vim.keymap.set('n', '<leader>ff', function()
  builtin.find_files({
    find_command = { 'rg', '--files', '--hidden', '--glob', '!.niebla/*' },
  })
end, { desc = 'Find files in Niebla workspace' })
```

---

## Emacs

### What to Expect
* Emacs default `vc-mode` attempts to locate `.git` or `.hg` in parent directory hierarchies. Disabling Git for Niebla workspace paths prevents startup delays.

### Recommended Emacs Configuration (`init.el` / `config.el`)

```elisp
;; Disable VC Git backend inside Niebla workspaces to prevent search overhead
(setq vc-handled-backends (delq 'Git vc-handled-backends))

;; Project.el: Define root markers for Niebla workspaces
(with-eval-after-load 'project
  (add-to-list 'project-find-functions
               (lambda (dir)
                 (when-let ((root (locate-dominating-file dir ".niebla")))
                   (cons 'transient root)))))

;; Projectile configuration (if using projectile)
(with-eval-after-load 'projectile
  (add-to-list 'projectile-project-root-files ".niebla")
  (add-to-list 'projectile-project-root-files "go.mod")
  (add-to-list 'projectile-project-root-files "Cargo.toml"))

;; Eglot / LSP-mode auto-detects project root using project.el
```

---

## Next Steps

* **[The Development Lifecycle](file:///home/matatan/Developer/niebla/docs-site/src/content/docs/workflows/development-lifecycle.md):** From editing files and CoW promotion to shelving and code review in Cotejo.
* **[Virtual Workspaces & Storage Economics](file:///home/matatan/Developer/niebla/docs-site/src/content/docs/workflows/workspaces.md):** Deep dive into FUSE overlays, CAS sharding, and disk economics.

