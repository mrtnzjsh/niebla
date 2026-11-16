---
title: IDE Setup
description: Configure VS Code, JetBrains IDEs (IntelliJ, GoLand, PyCharm), Neovim, and Emacs for Niebla workspaces.
sidebar:
  order: 4
  label: IDE Setup
---

Niebla workspaces mount as standard [POSIX](https://en.wikipedia.org/wiki/POSIX) directories, so editors and [language servers](https://en.wikipedia.org/wiki/Language_Server_Protocol) work out of the box. However, two adjustments will keep your IDE fast in large [monorepos](https://en.wikipedia.org/wiki/Monorepo):

1. **Scope file watchers and indexers:** In [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) mode, directory listings (`readdir`/`stat`) come from memory and unopened files take 0 bytes on disk—but if an IDE recursively reads every file in a 500,000-file workspace to build an index, it will trigger on-demand downloads for all of them.
2. **Disable Git polling:** Because Niebla does not use `.git` directories, disabling Git status polling prevents unnecessary parent directory scans.

---

## 1. Visual Studio Code

Add the following to `.vscode/settings.json` to disable Git polling, associate `.nieblaignore` with Git ignore syntax highlighting, and exclude internal scratch directories from file watchers:

```json
{
  "git.enabled": false,
  "git.autorefresh": false,
  "files.associations": {
    ".nieblaignore": "ignore"
  },
  "files.watcherExclude": {
    "**/.niebla/**": true,
    "**/scratch/**": true,
    "**/.git/**": true,
    "**/node_modules/**": true,
    "**/target/**": true,
    "**/dist/**": true
  }
}
```

---

## 2. JetBrains IDEs (IntelliJ IDEA, GoLand, PyCharm, CLion)

:::caution[Scope Indexing in FUSE Workspaces]
JetBrains IDEs eagerly read and index every file under the project root by default. In a large FUSE workspace, open only the target subdirectory or mark unneeded top-level directories as **Excluded** so IntelliJ does not download the entire monorepo into your local [CAS](https://en.wikipedia.org/wiki/Content-addressable_storage) cache.
:::

### 1. Scope Content Roots
* **Open a Subfolder Directly:** `goland ~/niebla/workspaces/auth-ui/services/auth`
* **Or Exclude Unneeded Directories:** In the **Project** panel (`Alt+1` / `Cmd+1`), right-click unused top-level folders and select **Mark Directory as → Excluded**.

### 2. Disable Git Directory Mapping
Open **Settings → Version Control → Directory Mappings** and set the VCS to **`<none>`**, or commit `.idea/vcs.xml`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<project version="4">
  <component name="VcsDirectoryMappings">
    <mapping directory="" vcs="" />
  </component>
</project>
```

### 3. Disable Safe Write (Recommended for FUSE Mode)
Under **Settings → Appearance & Behavior → System Settings → Synchronization**, uncheck **Use safe write**. This makes the IDE write directly to files rather than creating temporary swap files and renaming them over the original.

### 4. Associate `.nieblaignore` File Type
To enable standard `.gitignore` syntax highlighting and file icons in JetBrains IDEs:
1. Open **Settings → Editor → File Types**.
2. Select **`.ignore`** (or **`Git Ignore`** if using the `.ignore` plugin).
3. Under **File name patterns**, click **`+`** and add `.nieblaignore`.

---

## 3. Neovim

### 1. LSP & Telescope Workspace Detection
Configure `nvim-lspconfig` and Telescope to detect workspace roots using `.niebla` or language markers (`go.mod`, `Cargo.toml`) instead of `.git`:

```lua
local lspconfig = require('lspconfig')
local util = require('lspconfig.util')

lspconfig.gopls.setup({
  root_dir = util.root_pattern('go.work', 'go.mod', '.nieblaignore', '.niebla'),
})

lspconfig.rust_analyzer.setup({
  root_dir = util.root_pattern('Cargo.toml', '.nieblaignore', '.niebla'),
})

local builtin = require('telescope.builtin')
vim.keymap.set('n', '<leader>ff', function()
  builtin.find_files({
    find_command = { 'rg', '--files', '--hidden', '--glob', '!.niebla/*' },
  })
end, { desc = 'Find files in Niebla workspace' })
```

### 2. Filetype Detection for `.nieblaignore`
Add the following to `~/.config/nvim/filetype.lua` (or `after/filetype.lua`) to treat `.nieblaignore` as `gitignore`:

```lua
vim.filetype.add({
  filename = {
    ['.nieblaignore'] = 'gitignore',
  },
})
```

---

## 4. Emacs

### 1. Project Root Detection
Configure `project.el` (or `projectile`) to recognize `.niebla` as a project root marker:

```elisp
;; Recognize .niebla as a project root in project.el
(with-eval-after-load 'project
  (add-to-list 'project-find-functions
               (lambda (dir)
                 (when-let ((root (locate-dominating-file dir ".niebla")))
                   (cons 'transient root)))))

;; If using Projectile:
(with-eval-after-load 'projectile
  (add-to-list 'projectile-project-root-files ".niebla"))
```

### 2. Associate `.nieblaignore` with `gitignore-mode`
Add the following to `~/.emacs.d/init.el` or Doom Emacs `~/.config/doom/config.el`:

```elisp
(add-to-list 'auto-mode-alist '("\\.nieblaignore\\'" . gitignore-mode))
```

---

## 5. Helix

Configure Helix to highlight `.nieblaignore` as a Git ignore file by adding it to `~/.config/helix/languages.toml`:

```toml
[[language]]
name = "git-ignore"
file-types = ["gitignore", "nieblaignore"]
```

---

## 6. Universal In-File Modelines

If your team uses diverse editors and terminal environments, you can embed editor modeline comments at the very top of `.nieblaignore`. These activate `gitignore` syntax highlighting automatically without requiring manual per-user editor configuration:

```gitignore
# -*- mode: gitignore -*-
# vim: set ft=gitignore:
```

* **Vim / Neovim**: Detects `ft=gitignore` and activates the native gitignore syntax highlighter out of the box.
* **Emacs / Doom Emacs**: Detects `mode: gitignore` and enters `gitignore-mode` automatically upon opening.

---

## 7. Authoring `.nieblaignore` for Monorepos

Niebla workspaces do not automatically generate a `.nieblaignore` file upon creation so newly created workspaces start completely clean without untracked files. Instead, you create a single `.nieblaignore` file at the repository root and commit it in your initial changelist.

### Root-Only Monorepo Evaluation
Unlike Git, which searches every subdirectory for nested `.gitignore` files, Niebla evaluates ignore rules strictly from the repository root:
```
/.nieblaignore
```
When running `niebla status`, `niebla diff`, and `niebla shelve`, Niebla evaluates scratch paths against this root matcher and skips matching directories immediately via `filepath.SkipDir`. Nested ignore files inside subpackages are not evaluated.

### Built-in Default Patterns
Even before a `.nieblaignore` file is added, Niebla's engine automatically ignores baseline build, environment, and OS artifacts:
* `bazel-*`
* `.direnv/`
* `node_modules/`
* `/dist/`, `/tmp/`
* `.DS_Store`, `Thumbs.db`, `*.swp`, `*~`
* `.env.local`, `.env.*.local`

### Copy/Pasteable Starter `.nieblaignore` Template

Copy and paste the template below into `.nieblaignore` at the root of your workspace:

```gitignore
# -*- mode: gitignore -*-
# vim: set ft=gitignore:
#
# Monorepo-wide .nieblaignore
# Placed at the root of the workspace. Automatically inherited by all workspaces.

# --- Version Control & System Metadata ---
.DS_Store
Thumbs.db
*~
*.swp
*.swo

# --- Environment Variables & Local Secrets ---
.env
.env.local
.env.*.local
*.pem
*.key

# --- Build Systems & Outputs ---
dist/
build/
target/
result
result-*
bazel-*
out/

# --- Language Dependencies & Caches ---
# Node / JavaScript / TypeScript
node_modules/
.npm/
.yarn/
.turbo/

# Python
__pycache__/
*.py[cod]
*$py.class
.venv/
venv/
*.egg-info/

# Go & Rust
*.out
*.prof
*.test
debug/

# --- Editor Settings & Tooling Caches ---
.idea/
.direnv/
.cache/
# Allow shared VS Code configuration while ignoring user-specific state
.vscode/*
!.vscode/settings.json
!.vscode/tasks.json
!.vscode/launch.json
!.vscode/extensions.json
```
