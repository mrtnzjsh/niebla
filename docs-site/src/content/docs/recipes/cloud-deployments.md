---
title: "Production & Cloud Deployments"
description: Production deployment configurations for on-premise Linux servers, AWS, Google Cloud Platform (GCP), and Microsoft Azure.
sidebar:
  order: 3
---

For multi-developer engineering teams, run `niebla-server`, PostgreSQL, and `cotejo` on dedicated infrastructure—either on-premise Linux hosts or cloud virtual machines backed by managed PostgreSQL and object storage.

---

## 1. On-Premise Linux Production Setup

In a dedicated Linux deployment, configuration files live in `/etc/niebla/`, services run under an unprivileged `niebla` system user via hardened `systemd` units, and an NGINX or Caddy reverse proxy terminates TLS in front of Cotejo.

### Required Network Ports

| Service | Default Port | Protocol | Access Scope |
| :--- | :--- | :--- | :--- |
| **Reverse Proxy (HTTPS)** | `443` | TCP (HTTPS / TLS 1.3) | Developer workstations & browsers |
| **`niebla-server` (gRPC)** | `50051` | TCP (HTTP/2 + TLS) | Developer workstations & CI runners |
| **`cotejo` (Web UI)** | `8080` | TCP (HTTP) | Loopback only (`127.0.0.1`, behind proxy) |
| **PostgreSQL** | `5432` | TCP / Unix Socket | Internal (`niebla-server` only) |

### Hardened `systemd` Units

Create a dedicated system user and configuration directory:

```bash
sudo useradd --system --home-dir /var/lib/niebla --create-home --shell /usr/sbin/nologin niebla
sudo mkdir -p /etc/niebla /var/lib/niebla/blobs
sudo chown -R niebla:niebla /var/lib/niebla
```

**`/etc/systemd/system/niebla-server.service`:**
```ini
[Unit]
Description=Niebla Production gRPC Repository Server
After=network-online.target postgresql.service
Wants=network-online.target

[Service]
Type=simple
User=niebla
Group=niebla
EnvironmentFile=-/etc/niebla/secrets.env
ExecStart=/usr/local/bin/niebla-server --config /etc/niebla/config.toml
Restart=on-failure
RestartSec=5
LimitNOFILE=65536
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/niebla

[Install]
WantedBy=multi-user.target
```

**`/etc/systemd/system/cotejo.service`:**
```ini
[Unit]
Description=Cotejo Code Review Web Interface
After=network-online.target niebla-server.service
Requires=niebla-server.service

[Service]
Type=simple
User=niebla
Group=niebla
EnvironmentFile=-/etc/niebla/secrets.env
ExecStart=/usr/local/bin/cotejo --config /etc/niebla/config.toml
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true

[Install]
WantedBy=multi-user.target
```

### NGINX Reverse Proxy (with SSE Support)

Cotejo uses [Server-Sent Events (SSE)](https://en.wikipedia.org/wiki/Server-sent_events) (`/events`) to stream real-time review updates. Disable proxy buffering on `/events` so updates arrive immediately:

```nginx
server {
    listen 443 ssl http2;
    server_name review.example.com;

    ssl_certificate     /etc/letsencrypt/live/review.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/review.example.com/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /events {
        proxy_pass http://127.0.0.1:8080/events;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_cache off;
        chunked_transfer_encoding off;
        proxy_read_timeout 86400s;
    }
}
```

---

## 2. Cloud Provider Blueprints (AWS, GCP & Azure)

When deploying in a public cloud, combine a compute VM attached to high-[IOPS](https://en.wikipedia.org/wiki/IOPS) block storage (for [CAS](https://en.wikipedia.org/wiki/Content-addressable_storage) blobs) with managed PostgreSQL, an identity-aware load balancer (for Cotejo [SSO](https://en.wikipedia.org/wiki/Single_sign-on)), and encrypted object storage backups.

| Component | Amazon Web Services (AWS) | Google Cloud Platform (GCP) | Microsoft Azure |
| :--- | :--- | :--- | :--- |
| **Compute VM** | EC2 `c6i.2xlarge` (8 vCPU, 16 GB) | GCE `n2-standard-8` (8 vCPU, 32 GB) | VM `Standard_D8s_v5` (8 vCPU, 32 GB) |
| **CAS Block Disk** | EBS GP3 (`3,000+` IOPS, XFS/ext4) | Extreme PD or Local NVMe SSD | Premium SSD v2 (`3,000+` IOPS, XFS) |
| **Managed PostgreSQL** | Amazon RDS PostgreSQL (Multi-AZ) | Cloud SQL PostgreSQL (Regional HA) | Azure Database for PostgreSQL Flexible |
| **Cotejo SSO Proxy** | ALB + AWS Cognito / [OIDC](https://en.wikipedia.org/wiki/OpenID_Connect) | Cloud Load Balancer + IAP | Application Gateway + Entra ID |
| **SSO Identity Header** | `X-Amzn-Oidc-Identity` | `X-Goog-Authenticated-User-Email` | `X-Forwarded-User` |
| **Backup Target** | Amazon S3 (`[resilience.s3]`) | Google Cloud Storage (S3 Interop API) | Azure Blob Storage (`[resilience.sftp]`) |

---

### AWS `/etc/niebla/config.toml`

```toml
version = 1

[core]
server_addr = "127.0.0.1:50051"
default_workspace = "default"

[server]
host = "0.0.0.0"
port = 50051
db_url = "postgres://niebla_admin:PASSWORD@niebla-db.xyz.us-east-1.rds.amazonaws.com:5432/niebla?sslmode=verify-full&sslrootcert=/etc/ssl/certs/rds-global-bundle.pem"
auto_db = false
blob_path = "/var/lib/niebla/blobs"
cache_size = 50000
auth_tokens = ["sec_token_prod_eng_a8f9", "sec_token_ci_runners_3b1c"]

[critique]
host = "0.0.0.0"
port = 8080
proxy_port = 7331
required_approvals = 1
allow_submit_with_unresolved_comments = false
require_green_check_runs = true

[critique.auth]
mode = "headers"
header_name = "X-Amzn-Oidc-Identity"
trusted_proxies = ["10.0.1.0/24"] # ALB subnet CIDR

[resilience]
enabled = true
default_mode = "bundle"

[resilience.bundle]
enabled = true
target_dir = "/tmp/niebla-backups"
compression = "zstd"
compression_level = 3

[resilience.encryption]
algorithm = "aes-256-gcm"
key_source = "env"
key_env = "NIEBLA_MASTER_KEY"

[resilience.s3]
enabled = true
endpoint = "s3.us-east-1.amazonaws.com"
bucket = "niebla-prod-backups"
region = "us-east-1"
prefix = "monorepo/"
access_key_env = "AWS_ACCESS_KEY_ID"
secret_key_env = "AWS_SECRET_ACCESS_KEY"
use_ssl = true
storage_class = "INTELLIGENT_TIERING"

[resilience.schedule]
auto_backup = true
interval_hours = 4
retention_count = 30
snapshot_on_submit = true
```

---

### GCP `/etc/niebla/config.toml`

On GCP, point `db_url` to your Cloud SQL private IP, configure `[critique.auth]` for Google Cloud Identity-Aware Proxy (IAP), and use GCS's S3-compatible interoperability endpoint (`storage.googleapis.com`):

```toml
[server]
host = "0.0.0.0"
port = 50051
db_url = "postgres://niebla_user:PASSWORD@10.128.0.10:5432/niebla?sslmode=verify-ca&sslrootcert=/etc/niebla/tls/server-ca.pem"
auto_db = false
blob_path = "/var/lib/niebla/blobs"
cache_size = 50000

[critique.auth]
mode = "headers"
header_name = "X-Goog-Authenticated-User-Email"
trusted_proxies = ["130.211.0.0/22", "35.191.0.0/16"] # GCP IAP / Load Balancer ranges

[resilience.s3]
enabled = true
endpoint = "storage.googleapis.com"
bucket = "niebla-corp-backups"
region = "auto"
prefix = "niebla/"
access_key_env = "GCS_HMAC_ACCESS_KEY"
secret_key_env = "GCS_HMAC_SECRET_KEY"
use_ssl = true
```

---

### Azure `/etc/niebla/config.toml`

On Azure, point `db_url` to Azure Database for PostgreSQL Flexible Server, configure `[critique.auth]` for Azure Application Gateway (Entra ID), and ship encrypted bundles via `[resilience.sftp]` to Azure Blob Storage SFTP:

```toml
[server]
host = "0.0.0.0"
port = 50051
db_url = "postgres://niebla_admin:PASSWORD@niebla-pg-flex.postgres.database.azure.com:5432/niebla?sslmode=require"
auto_db = false
blob_path = "/var/lib/niebla/blobs"
cache_size = 50000

[critique.auth]
mode = "headers"
header_name = "X-Forwarded-User"
trusted_proxies = ["10.0.0.0/24"] # Application Gateway subnet

[resilience.sftp]
enabled = true
host = "nieblabackups.blob.core.windows.net"
user = "nieblabackups.backupuser"
remote_path = "/backups/monorepo"
key_file = "/etc/niebla/ssh/azure_backup_id_ed25519"
timeout_seconds = 120
```
