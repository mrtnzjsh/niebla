---
title: Installation & Setup Matrix
description: Multi-platform installation guides for NixOS, macOS, generic Linux, and Docker.
---

import { Tabs, TabItem } from '@astrojs/starlight/components';

Choose your operating system and deployment profile below.

<Tabs>
  <TabItem label="NixOS (Recommended)">
    Add the `niebla` input to your system flake:

    ```nix
    inputs.niebla = {
      url = "github:mrtnzjsh/niebla";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    ```

    Enable the full server, review suite, and isolated PostgreSQL database:

    ```nix
    imports = [ inputs.niebla.nixosModules.default ];

    services.niebla = {
      enable = true;
      postgres.enable = true; # Dedicated DB on port 5433
      server.enable = true;   # gRPC on port 50051
      cotejo.enable = true;   # Review UI on port 8080
      client.enable = true;   # Installs niebla & nieblafs
    };
    ```
  </TabItem>

  <TabItem label="macOS (Darwin)">
    For macOS workstations targeting a remote Niebla server (such as `nixdev:50051`):

    ### Via Home Manager (`programs.niebla`)
    ```nix
    imports = [ inputs.niebla.homeManagerModules.default ];

    programs.niebla = {
      enable = true;
      enableSkills = true;
    };
    ```

    ### Direct Materialization Mode (`--no-fuse`)
    macOS developers can create workspaces without needing third-party kernel extensions:

    ```bash
    niebla workspace create my-feature --no-fuse --server nixdev:50051
    ```
  </TabItem>

  <TabItem label="Generic Linux">
    Download the latest binary release for `x86_64-linux` or compile from source:

    ```bash
    git clone https://github.com/mrtnzjsh/niebla.git
    cd niebla/niebla
    make build
    ```

    The compiled binaries are available in `niebla/bin/`:
    - `niebla` (CLI client)
    - `nieblafs` (FUSE workspace daemon)
    - `niebla-server` (gRPC repository daemon)
    - `cotejo` (Web code review server)
  </TabItem>
</Tabs>
