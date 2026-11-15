---
title: "Security, Privacy & Authentication"
description: Overview of Niebla's open-source privacy guarantees, zero-telemetry design, gRPC bearer authentication, TLS 1.3 encryption, Cotejo SSO, and backup encryption.
sidebar:
  order: 7
---

Niebla is 100% open-source software (MIT License) designed for self-hosted, security-sensitive engineering environments.

---

## 1. Open Source Transparency & Zero Telemetry

> [!IMPORTANT]
> **Niebla never phones home.** No binary, daemon, or web interface in the Niebla toolchain collects telemetry, usage analytics, crash reports, or repository metrics for external use.

* **No External Network Calls:** By default, `niebla`, `niebla-server`, `nieblafs`, and `cotejo` communicate exclusively over local loopback (`127.0.0.1`) or the private server address you configure.
* **No Hidden Services or License Checks:** Every line of code is inspectable in the public repository and buildable from source via `nix build`.
* **Opt-In Integrations Only:** Outbound connections to AI inference servers (`[critique.ai]`), CI runners (`[ci]`), CloudEvents webhooks (`[events]`), or backup targets (`[resilience]`) are disabled by default and only contact endpoints you explicitly configure.

---

## 2. Security Trust Zones

Niebla partitions authentication and encryption across five operational boundaries:

| Trust Zone | Protocol & Default Port | Authentication & Encryption | Configuration Section |
| :--- | :--- | :--- | :--- |
| **1. CLI & VFS to Server** | [gRPC](https://en.wikipedia.org/wiki/GRPC) (HTTP/2) `:50051` | Bearer tokens (`authorization` / `x-niebla-token`) + [TLS 1.3](https://en.wikipedia.org/wiki/Transport_Layer_Security#TLS_1.3) / [mTLS](https://en.wikipedia.org/wiki/Mutual_authentication#mTLS) | `[server]`, `[core.tls]` |
| **2. Cotejo Web UI** | HTTP / [SSE](https://en.wikipedia.org/wiki/Server-sent_events) `:8080` | Local `$USER`, Tailscale WhoIs, or Trusted Proxy [SSO](https://en.wikipedia.org/wiki/Single_sign-on) Headers | `[critique.auth]` |
| **3. CI Runner Dispatch** | HTTPS / Subprocess | Bearer token, Basic Auth, Custom Header, or [HMAC](https://en.wikipedia.org/wiki/HMAC) signature | `[ci.runners.<name>.auth]` |
| **4. CloudEvents Webhooks** | HTTP `POST` | HMAC-SHA256 payload signature (`X-Niebla-Signature`) | `[events.webhook]` |
| **5. Backups at Rest** | [ZFS](https://en.wikipedia.org/wiki/ZFS) / S3 / SFTP | [AES-256-GCM](https://en.wikipedia.org/wiki/Galois/Counter_Mode) or [ChaCha20-Poly1305](https://en.wikipedia.org/wiki/ChaCha20-Poly1305) envelope encryption | `[resilience.encryption]` |

---

## 3. gRPC Transport Security (`niebla-server`)

### Bearer Token Authentication

When `server.auth_tokens` is non-empty, `niebla-server` enforces [constant-time](https://en.wikipedia.org/wiki/Timing_attack) token verification (`crypto/subtle.ConstantTimeCompare`) across all unary and streaming gRPC calls:

```toml
# /etc/niebla/config.toml (Server)
[server]
host = "0.0.0.0"
port = 50051
auth_tokens = [
  "nb_live_dev_8f21a09d3b44e7c1",
  "nb_live_ci_4917a2ef819c4d92"
]
```

Clients supply their token via the `NIEBLA_TOKEN` environment variable (recommended) or `core.auth_token` in `~/.config/niebla/config.toml`:

```bash
export NIEBLA_TOKEN="nb_live_dev_8f21a09d3b44e7c1"
niebla status
```

### TLS 1.3 & Mutual TLS (mTLS)

To encrypt gRPC traffic over untrusted networks and optionally require client certificates (mTLS):

```toml
# Server TLS configuration
[server.tls]
enabled = true
cert_file = "/etc/niebla/tls/server.crt"
key_file = "/etc/niebla/tls/server.key"
client_ca_file = "/etc/niebla/tls/client-ca.crt" # Optional: enables mTLS

# Client TLS configuration
[core.tls]
enabled = true
ca_file = "/etc/niebla/tls/ca.crt"
server_name = "niebla.internal"
```

---

## 4. Cotejo Web Authentication

Cotejo supports three authentication modes under `[critique.auth]`:

1. **`mode = "local"`:** Uses `default_user` (`$USER`) for single-developer loopback setups.
2. **`mode = "tailscale"`:** Queries `/run/tailscale/tailscaled.sock` via Tailscale WhoIs to authenticate peers on your private [WireGuard](https://en.wikipedia.org/wiki/WireGuard) tailnet automatically (see [Tailscale Private Mesh](/niebla/recipes/tailscale/)).
3. **`mode = "headers"`:** Extracts verified SSO identities (such as `X-Forwarded-User`, `X-Amzn-Oidc-Identity`, or `X-Goog-Authenticated-User-Email`) injected by reverse proxies whose source IP matches `trusted_proxies`. Requests from untrusted IPs have their identity headers stripped to prevent spoofing (see [Deployment & Authentication](/niebla/cotejo/deployment-auth/)).

---

## 5. CI Runner & Webhook Credentials

### Outbound CI Runner Auth (`[ci.runners.<name>.auth]`)

Never hardcode secrets in committed `.toml` files. Reference environment variables (`token_env`, `password_env`, `secret_env`) or mounted secret files (`key_file`):

```toml
[ci.runners.forgejo-linux.auth]
mode = "token"
token_env = "FORGEJO_TOKEN"

[ci.runners.custom-webhook.auth]
mode = "hmac"
secret_env = "RUNNER_HMAC_SECRET"
```

### CloudEvents Webhook Verification

Outbound CloudEvents webhooks sign the raw HTTP request body with HMAC-SHA256 in the `X-Niebla-Signature` header. See [CloudEvents & Webhooks](/niebla/reference/events/#4-hmac-sha256-signature-verification) for Go and Python verification functions.

---

## 6. At-Rest Backup Encryption

Portable backup bundles (`niebla backup create --mode bundle`) are encrypted prior to upload using authenticated **AES-256-GCM** or **ChaCha20-Poly1305** with a random 12-byte [nonce](https://en.wikipedia.org/wiki/Cryptographic_nonce) per archive:

```toml
[resilience.encryption]
algorithm = "aes-256-gcm" # or "chacha20-poly1305"
key_source = "env"        # "env", "file", or "passphrase"
key_env = "NIEBLA_MASTER_KEY"
```

For ZFS-backed deployments, setting `raw_encrypted_send = true` under `[resilience.zfs]` streams native ZFS encrypted datasets (`zfs send -w`) so standby storage nodes replicate blocks without needing the dataset decryption key loaded in memory.

---

## 7. Production Security Checklist

| Check | Requirement | Verification Command |
| :--- | :--- | :--- |
| **gRPC Token Auth** | `server.auth_tokens` configured with random tokens | `niebla config show --resolved` |
| **Network Binding** | `niebla-server` and `cotejo` bound to loopback, private VLAN, or Tailscale IP | `ss -tulpn \| grep -E '50051\|8080'` |
| **Proxy CIDR Allowlist** | `critique.auth.trusted_proxies` restricted to your reverse proxy IPs | `niebla config validate` |
| **Secret Hygiene** | Credentials passed via `*_env` variables or `EnvironmentFile`, never plain text | `grep -rn "token =" ~/.config/niebla/` |
| **Read-Only CAS Cache** | Local CAS blobs materialized with `0444` permissions to prevent mutation | `ls -l ~/.cache/niebla/blobs/` |
