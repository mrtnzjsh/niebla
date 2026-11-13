---
title: Deploying over Tailscale
description: Accessing niebla-server and Cotejo securely across your private tailnet.
---

By combining Niebla with Tailscale, you can access your remote repository server (`niebla-server` on port 50051) and web code review interface (`cotejo` on port 8080) from your laptop or MacBook without exposing ports to the public internet.

## 1. Configure the Remote Server

Ensure Tailscale is enabled in your NixOS configuration:

```nix
services.tailscale.enable = true;

services.niebla = {
  enable = true;
  server.host = "0.0.0.0";
  cotejo.host = "0.0.0.0";
};
```

---

## 2. Connect from your Laptop

Once connected to your Tailnet, point your local CLI to your server's hostname or MagicDNS address (`<server-host>`):

```bash
niebla --server <server-host>:50051 workspace create my-feature --no-fuse
```

Open Cotejo review in your laptop browser:

```
http://<server-host>:8080
```

