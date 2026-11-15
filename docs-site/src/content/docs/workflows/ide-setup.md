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

Add the following to `.vscode/settings.json` to disable Git polling and exclude internal scratch directories from file watchers:

```json
{
  "git.enabled": false,
  "git.autorefresh": false,
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

---

## 3. Neovim

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

---

## 4. Emacs

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
