---
title: Authentication & Theming
description: Configure identity resolution, real-time Server-Sent Events (SSE), and color themes for the Cotejo web review UI.
sidebar:
  order: 4
  label: Authentication & Theming
---

This guide covers how to configure user authentication, real-time browser updates, and visual themes for **Cotejo**.

For production systemd service units and NGINX/cloud load balancer setups, see [Production & Cloud Deployments](/niebla/recipes/cloud-deployments/) and [Security & Authentication](/niebla/reference/security-auth/).

---

## 1. Authentication Modes (`[critique.auth]`)

Cotejo resolves the active reviewer identity according to `[critique.auth]` in `~/.config/niebla/config.toml`:

| Mode | Use Case | How Identity Is Resolved |
| :--- | :--- | :--- |
| `local` / `developer` | Single-developer workstations | Uses `default_user` (defaults to `$USER`). |
| `headers` | Reverse proxies (NGINX, Caddy, Cloudflare Access, Cloud IAP) | Reads `header_name` (e.g., `X-Forwarded-User`) from requests arriving from `trusted_proxies`. |
| `tailscale` | Private Tailscale mesh networks | Queries `/run/tailscale/tailscaled.sock` (WhoIs API) to verify the peer's Tailscale login. See [Tailscale Private Mesh](/niebla/recipes/tailscale/). |
| `oidc` | Direct OAuth2 / [OIDC](https://en.wikipedia.org/wiki/OpenID_Connect) federation | Validates `Authorization: Bearer <jwt>` ([JWT](https://en.wikipedia.org/wiki/JSON_Web_Token)) against your OIDC provider's JWKS endpoint. |

### Example: Reverse Proxy Header Auth
```toml
[critique.auth]
mode = "headers"
header_name = "X-Forwarded-User"
default_user = "nobody"
trusted_proxies = ["127.0.0.1/32", "10.0.0.0/8"]
allow_query_override = false
```

:::caution[Reverse Proxy SSE Buffering]
Cotejo pushes real-time review updates over [Server-Sent Events (SSE)](https://en.wikipedia.org/wiki/Server-sent_events) (`GET /events`). When placing NGINX in front of Cotejo, disable proxy buffering (`proxy_buffering off; proxy_cache off;`) so SSE frames stream immediately to connected browsers.
:::

---

## 2. Visual Themes & Syntax Highlighting (`[critique.theme]`)

Cotejo ships with 8 built-in color presets and supports custom hex/RGBA palettes in `~/.config/niebla/config.toml` (or `~/.config/niebla/theme.toml`).

### Using a Built-In Preset
```toml
[critique.theme]
# Presets: "catppuccin-mocha", "catppuccin-macchiato", "catppuccin-frappe",
#          "catppuccin-latte", "tokyo-night", "dracula", "nord", "github-dark", "custom"
preset = "catppuccin-mocha"
syntax_style = "catppuccin-mocha"
```

### Custom Color Overrides
```toml
[critique.theme]
preset = "custom"
syntax_style = "tokyonight-night"

[critique.theme.colors]
bg_canvas       = "#0f172a"
bg_surface      = "#1e293b"
bg_card         = "#334155"
border          = "#475569"
text_primary    = "#f8fafc"
text_muted      = "#94a3b8"
accent          = "#2dd4bf"
diff_add_bg     = "rgba(45, 212, 191, 0.18)"
diff_add_border = "#2dd4bf"
diff_del_bg     = "rgba(244, 63, 94, 0.18)"
diff_del_border = "#f43f5e"
```

Run `niebla config validate` after editing to verify that all color tokens and Chroma syntax style names are valid.
