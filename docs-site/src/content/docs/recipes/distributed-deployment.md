---
title: "Distributed On-Premise & Enterprise Deployment"
description: Production-grade deployment guide for running Niebla across dedicated storage, high-availability PostgreSQL, and Cotejo review gateway tiers on bare-metal or private infrastructure.
sidebar:
  order: 3
  label: Distributed On-Premise Deployment
---

For enterprise teams managing millions of lines of code and hundreds of engineers, Niebla scales horizontally across dedicated infrastructure tiers.

In this architecture:
- **Storage & Repository Tier:** Dedicated high-performance Linux servers run `niebla-server` backed by enterprise NVMe drives or ZFS storage pools for Content-Addressable Storage (CAS).
- **Database Tier:** A high-availability PostgreSQL cluster (with streaming replication or Patroni) stores changelist metadata, file trees, and review threads.
- **Review & Gateway Tier:** `cotejo` instances run behind an NGINX or Caddy reverse proxy that handles TLS termination, corporate SSO authentication, and load balancing.
- **Resilience & Standby Tier:** Remote storage servers receive continuous raw encrypted ZFS snapshots over a dedicated 10GbE storage network.

```mermaid
flowchart TD
    subgraph Clients["Developer Workstations & CI Runners"]
        CLI1["Developer Laptop<br/>(macOS)"]
        CLI2["Linux Workstation<br/>(FUSE)"]
        CI["CI Runners<br/>(Forgejo / GitHub)"]
    end

    subgraph Gateway["Gateway & Review Tier (:443)"]
        LB["NGINX / Caddy Reverse Proxy<br/>(TLS & Corporate SSO / OIDC)"]
        Cotejo["Cotejo Web Review Instances<br/>(Port 8080)"]
        LB --> Cotejo
    end

    subgraph CoreTier["Core Repository Tier (:50051)"]
        Server["niebla-server Daemon<br/>(gRPC Server Engine)"]
        CAS[("High-Speed NVMe / ZFS Pool<br/><code>/devpool/niebla_blobs</code>")]
        Server --> CAS
    end

    subgraph DataTier["Data & Disaster Recovery Tier"]
        PG[("HA PostgreSQL Cluster<br/>(Port 5432)")]
        DR[("Standby ZFS Replica Node<br/>(10GbE Raw Streaming)")]
    end

    CLI1 -->|"gRPC :50051"| Server
    CLI2 -->|"gRPC :50051"| Server
    CI -->|"gRPC :50051"| Server
    Clients -->|"HTTPS :443"| LB
    Cotejo -->|"RPC"| Server
    Server -->|"Metadata"| PG
    CAS -.->|"10GbE zfs send"| DR
```

---

## 1. Modular Server Configuration (`/etc/niebla/*.toml`)

On enterprise servers, decouple configuration by operational concern using Niebla's native multi-file discovery in `/etc/niebla/`.

Set the system-wide environment variable:
```bash
# In /etc/environment or systemd service unit:
NIEBLA_CONFIG=/etc/niebla
```

### File 1: `/etc/niebla/config.toml` (Core Server & Storage)
```toml
# /etc/niebla/config.toml
version = 1

[core]
server_addr = "10.10.0.10:50051"
default_workspace = "default"

[server]
host = "0.0.0.0"
port = 50051
# Dedicated PostgreSQL cluster connection
db_url = "postgres://niebla_app:SecretPassword123@10.10.0.20:5432/niebla?sslmode=verify-full&sslrootcert=/etc/niebla/tls/db-ca.crt"
auto_db = false
# Dedicated NVMe ZFS storage mount
blob_path = "/devpool/niebla_blobs"
cache_size = 50000

# Client authorization tokens
auth_tokens = [
  "sec_token_eng_general_4b9a",
  "sec_token_ci_runners_8f1c"
]

[server.tls]
enabled = true
cert_file = "/etc/niebla/tls/server.crt"
key_file = "/etc/niebla/tls/server.key"
client_ca_file = "/etc/niebla/tls/client-ca.crt"
```

### File 2: `/etc/niebla/auth.toml` (Cotejo & SSO Identity)
```toml
# /etc/niebla/auth.toml
[critique]
host = "127.0.0.1"
port = 8080
proxy_port = 7331
default_reviewer = "$USER"
required_approvals = 1
allow_submit_with_unresolved_comments = false
require_green_check_runs = true

[critique.auth]
# Extract verified employee username from NGINX reverse proxy headers
mode = "headers"
header_name = "X-Forwarded-User"
trusted_proxies = ["127.0.0.1/32", "10.10.0.0/16"]
allow_query_override = false

[critique.groups]
mode = "static"

[critique.groups.static]
core-infra = ["alice", "bob", "carol"]
platform-leads = ["david", "elena"]
```

### File 3: `/etc/niebla/resilience.toml` (10GbE ZFS Replication)
```toml
# /etc/niebla/resilience.toml
[resilience]
enabled = true
default_mode = "zfs_stream"

[resilience.zfs]
enabled = true
local_dataset = "devpool/niebla_blobs"
remote_host = "dr-node.storage.internal"
remote_user = "niebla-repl"
remote_ssh_port = 22
remote_key_file = "/etc/niebla/ssh/id_ed25519"
remote_dataset = "backuppool/niebla_replica"
raw_encrypted_send = true
snapshot_prefix = "niebla-auto"
retention_count = 60
bandwidth_limit_mb = 0 # Full 10GbE line rate

[resilience.schedule]
auto_backup = true
interval_hours = 2
retention_count = 60
snapshot_on_submit = true
```

Validate the modular configuration directory:

```console
$ niebla config validate /etc/niebla
✓ Configuration is valid (/etc/niebla: config.toml, auth.toml, resilience.toml)
```

---

## 2. Production Systemd Services

Create hardened systemd units with dedicated system users and elevated file descriptor limits.

### Create System User
```bash
sudo useradd -r -s /usr/sbin/nologin -d /var/empty niebla
sudo chown -R niebla:niebla /devpool/niebla_blobs /etc/niebla
```

### `/etc/systemd/system/niebla-server.service`
```ini
[Unit]
Description=Niebla Enterprise Repository Daemon
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=niebla
Group=niebla
Environment=NIEBLA_CONFIG=/etc/niebla
ExecStart=/usr/local/bin/niebla-server
Restart=always
RestartSec=5s

# Enterprise Resource & File Limits
LimitNOFILE=65536
LimitNPROC=32768
LimitMEMLOCK=infinity
TasksMax=infinity

# Security Sandboxing
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/devpool/niebla_blobs /tmp
PrivateTmp=true
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

### `/etc/systemd/system/cotejo.service`
```ini
[Unit]
Description=Cotejo Enterprise Code Review Web Application
After=network.target niebla-server.service
Requires=niebla-server.service

[Service]
Type=simple
User=niebla
Group=niebla
Environment=NIEBLA_CONFIG=/etc/niebla
ExecStart=/usr/local/bin/cotejo
Restart=always
RestartSec=3s

LimitNOFILE=32768
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

Enable and start services:
```bash
sudo systemctl daemon-reload
sudo systemctl enable --now niebla-server cotejo
```

---

## 3. Reverse Proxy Configuration (NGINX with SSO)

Place Cotejo behind an NGINX reverse proxy to terminate TLS, proxy WebSockets, and enforce corporate Single Sign-On (e.g., Authelia, Pomerium, Cloudflare Access, or Keycloak) by injecting `X-Forwarded-User`.

```nginx
# /etc/nginx/sites-available/cotejo.conf
upstream cotejo_backend {
    server 127.0.0.1:8080;
    keepalive 32;
}

server {
    listen 443 ssl http2;
    server_name cotejo.corp.internal;

    ssl_certificate /etc/letsencrypt/live/cotejo.corp.internal/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/cotejo.corp.internal/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;

    # Corporate SSO Authentication Endpoint (e.g. Authelia)
    # Forward auth validates user identity and sets $remote_user
    include /etc/nginx/snippets/authelia-authrequest.conf;

    location / {
        proxy_pass http://cotejo_backend;
        proxy_http_version 1.1;

        # WebSocket & live reload headers
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        # Pass verified employee identity to Cotejo
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-User $remote_user;

        # Timeouts for large diff rendering
        proxy_read_timeout 120s;
        proxy_send_timeout 120s;
    }
}
```

---

## 4. Network Firewall Rules

| Port | Protocol | Source | Purpose |
| :--- | :--- | :--- | :--- |
| `50051/tcp` | gRPC / TLS | Developer Subnet (`10.10.0.0/16`) | Merkle tree synchronization & blob uploads. |
| `443/tcp` | HTTPS | Corporate Network (`0.0.0.0/0` via VPN) | Cotejo web code review and inline comments. |
| `5432/tcp` | PostgreSQL | Only `niebla-server` host (`10.10.0.10/32`) | Monorepo metadata database queries. |
| `22/tcp` | SSH | Only dedicated storage network (`10.20.0.0/24`) | 10GbE ZFS snapshot replication stream. |

