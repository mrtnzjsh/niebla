---
title: "Cloud Deployments: AWS, GCP & Azure"
description: Production-grade cloud deployment recipes for Amazon Web Services, Google Cloud Platform, and Microsoft Azure using managed databases, object storage, and IAM authentication.
sidebar:
  order: 4
  label: Cloud Deployments (AWS, GCP, Azure)
---

Deploying Niebla in major public cloud providers allows teams to eliminate on-premise hardware management by leveraging managed relational databases (RDS, Cloud SQL, Azure Database for PostgreSQL), scalable object storage (S3, GCS, Azure Blob), and identity-aware reverse proxies (ALB + Cognito, GCP IAP, Azure App Gateway + Entra ID).

Below are production blueprints and configuration templates for AWS, GCP, and Azure.

---

## 1. Amazon Web Services (AWS)

```mermaid
flowchart TD
    subgraph AWS["Amazon Web Services (AWS VPC)"]
        subgraph PublicSubnet["Public Subnet"]
            ALB["Application Load Balancer (ALB)<br/>(AWS Cognito OIDC Auth)"]
        end

        subgraph PrivateSubnet["Private Subnet"]
            EC2["EC2 Instance (c6i.xlarge)<br/><code>niebla-server</code> &amp; <code>cotejo</code>"]
            EBS[("EBS GP3 Volume<br/><code>/devpool/niebla_blobs</code>")]
            RDS[("Amazon RDS PostgreSQL<br/>(Multi-AZ)")]
            EC2 --> EBS
            EC2 --> RDS
        end

        subgraph S3Bucket["Object Storage"]
            S3[("Amazon S3 Bucket<br/>(Encrypted Backups)")]
        end

        ALB -->|"X-Amzn-Oidc-Identity"| EC2
        EC2 -.->|"Automated Snapshots"| S3
    end
```

### AWS Architecture Overview
- **Compute:** EC2 instance (`c6i.2xlarge` or `m6i.2xlarge`, Amazon Linux 2023 or Ubuntu 24.04 LTS). Attach an EBS GP3 volume with 3,000 baseline IOPS and 125 MB/s throughput formatted with XFS or ext4 for Content-Addressable Storage (CAS) at `/devpool/niebla_blobs`.
- **Database:** Amazon RDS for PostgreSQL 15+ (db.m6g.xlarge, Multi-AZ enabled).
- **Authentication:** AWS Application Load Balancer (ALB) integrated with AWS Cognito User Pools or corporate Okta/OIDC. The ALB injects `X-Amzn-Oidc-Identity` into Cotejo requests.
- **Resilience:** Amazon S3 bucket with lifecycle policies transitioning snapshots to S3 Glacier Flexible Retrieval.

### AWS Configuration (`~/.config/niebla/config.toml`)

```toml
# /etc/niebla/config.toml (AWS Production)
version = 1

[core]
server_addr = "10.0.2.50:50051"
default_workspace = "default"

[server]
host = "0.0.0.0"
port = 50051
# Amazon RDS PostgreSQL private endpoint
db_url = "postgres://niebla_admin:RDSSecretPassword@niebla-db.c9abcdef.us-east-1.rds.amazonaws.com:5432/niebla?sslmode=verify-full&sslrootcert=/etc/ssl/certs/rds-global-bundle.pem"
auto_db = false
# Mounted Amazon EBS GP3 volume
blob_path = "/devpool/niebla_blobs"
cache_size = 50000

auth_tokens = [
  "sec_token_prod_eng_a8f9",
  "sec_token_ci_runners_3b1c"
]

[critique]
host = "0.0.0.0"
port = 8080
proxy_port = 7331
default_reviewer = "$USER"
required_approvals = 1
allow_submit_with_unresolved_comments = false
require_green_check_runs = true

[critique.auth]
# ALB injects the verified Cognito user identity header
mode = "headers"
header_name = "X-Amzn-Oidc-Identity"
trusted_proxies = ["10.0.1.0/24"] # Public subnet containing ALB

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
bucket = "niebla-prod-backups-123456789012"
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

## 2. Google Cloud Platform (GCP)

```mermaid
flowchart TD
    subgraph GCP["Google Cloud Platform (GCP VPC)"]
        subgraph CloudIAP["Identity-Aware Proxy (IAP)"]
            GLB["Cloud Load Balancing<br/>(Google Workspace SSO)"]
        end

        subgraph PrivateVPC["Private Compute Subnet"]
            GCE["Compute Engine VM (n2-standard-8)<br/><code>niebla-server</code> &amp; <code>cotejo</code>"]
            SSD[("Extreme Persistent Disk<br/><code>/devpool/niebla_blobs</code>")]
            CloudSQL[("Cloud SQL for PostgreSQL<br/>(HA Private IP)")]
            GCE --> SSD
            GCE --> CloudSQL
        end

        subgraph Storage["Cloud Storage"]
            GCS[("Google Cloud Storage (GCS)<br/>(S3 Interoperability API)")]
        end

        GLB -->|"X-Goog-Authenticated-User-Email"| GCE
        GCE -.->|"Automated Snapshots"| GCS
    end
```

### GCP Architecture Overview
- **Compute:** Compute Engine VM (`n2-standard-8` or `c2-standard-8`, Debian 12 / Ubuntu 24.04). Attach an Extreme Persistent Disk or Local NVMe SSD formatted with ext4 at `/devpool/niebla_blobs`.
- **Database:** Cloud SQL for PostgreSQL 15+ configured with Private Services Access (private IP) and High Availability (regional failover).
- **Authentication:** Cloud Load Balancing configured with **Identity-Aware Proxy (IAP)**. IAP enforces Google Workspace corporate login (including MFA) and forwards `X-Goog-Authenticated-User-Email` directly to Cotejo.
- **Resilience:** Google Cloud Storage (GCS) bucket configured with interoperability keys (S3-compatible API endpoint `storage.googleapis.com`).

### GCP Configuration (`~/.config/niebla/config.toml`)

```toml
# /etc/niebla/config.toml (GCP Production)
version = 1

[core]
server_addr = "10.128.0.2:50051"
default_workspace = "default"

[server]
host = "0.0.0.0"
port = 50051
# Google Cloud SQL Private IP endpoint
db_url = "postgres://niebla_user:CloudSQLPassword@10.128.0.10:5432/niebla?sslmode=verify-ca&sslrootcert=/etc/niebla/tls/server-ca.pem"
auto_db = false
blob_path = "/devpool/niebla_blobs"
cache_size = 50000

[critique]
host = "0.0.0.0"
port = 8080
proxy_port = 7331
default_reviewer = "$USER"
required_approvals = 1
allow_submit_with_unresolved_comments = false
require_green_check_runs = true

[critique.auth]
# Identity-Aware Proxy (IAP) injects verified corporate Google email
mode = "headers"
header_name = "X-Goog-Authenticated-User-Email"
trusted_proxies = ["130.211.0.0/22", "35.191.0.0/16"] # GCP Load Balancer IP ranges

[resilience]
enabled = true
default_mode = "bundle"

[resilience.s3]
# GCS S3-compatible interoperability API endpoint
enabled = true
endpoint = "storage.googleapis.com"
bucket = "niebla-corp-backups"
region = "auto"
prefix = "niebla/"
access_key_env = "GCS_HMAC_ACCESS_KEY"
secret_key_env = "GCS_HMAC_SECRET_KEY"
force_path_style = false
use_ssl = true

[resilience.schedule]
auto_backup = true
interval_hours = 4
retention_count = 30
snapshot_on_submit = true
```

---

## 3. Microsoft Azure

```mermaid
flowchart TD
    subgraph Azure["Microsoft Azure (VNet)"]
        subgraph GatewaySubnet["App Gateway Subnet"]
            AppGW["Azure Application Gateway<br/>(Microsoft Entra ID / Azure AD SSO)"]
        end

        subgraph BackendSubnet["Private Virtual Network Subnet"]
            VM["Azure VM (Standard_D8s_v5)<br/><code>niebla-server</code> &amp; <code>cotejo</code>"]
            Disk[("Premium SSD v2<br/><code>/devpool/niebla_blobs</code>")]
            AzurePG[("Azure Database for PostgreSQL<br/>(Flexible Server - Zone Redundant)")]
            VM --> Disk
            VM --> AzurePG
        end

        subgraph BlobStorage["Storage Account"]
            Blob[("Azure Blob Storage<br/>(SFTP / WebDAV Archive)")]
        end

        AppGW -->|"X-Forwarded-User"| VM
        VM -.->|"Encrypted Backups"| Blob
    end
```

### Azure Architecture Overview
- **Compute:** Azure Virtual Machine (`Standard_D8s_v5` or `Standard_E8s_v5`, Ubuntu 24.04 LTS). Attach a Premium SSD v2 managed data disk formatted with XFS mounted at `/devpool/niebla_blobs`.
- **Database:** Azure Database for PostgreSQL Flexible Server configured with Zone-Redundant High Availability and Private Endpoint integration.
- **Authentication:** Azure Application Gateway with Web Application Firewall (WAF) integrated with Microsoft Entra ID (formerly Azure AD). The gateway terminates TLS, authenticates employees, and forwards `X-Forwarded-User` or `X-MS-CLIENT-PRINCIPAL-NAME`.
- **Resilience:** Azure Blob Storage account configured with SFTP or WebDAV endpoints for automated off-site encrypted bundle archives.

### Azure Configuration (`~/.config/niebla/config.toml`)

```toml
# /etc/niebla/config.toml (Azure Production)
version = 1

[core]
server_addr = "10.0.1.4:50051"
default_workspace = "default"

[server]
host = "0.0.0.0"
port = 50051
# Azure Database for PostgreSQL Flexible Server private endpoint
db_url = "postgres://niebla_admin:AzureStrongPassword123@niebla-pg-flex.postgres.database.azure.com:5432/niebla?sslmode=require"
auto_db = false
blob_path = "/devpool/niebla_blobs"
cache_size = 50000

[critique]
host = "0.0.0.0"
port = 8080
proxy_port = 7331
default_reviewer = "$USER"
required_approvals = 1
allow_submit_with_unresolved_comments = false
require_green_check_runs = true

[critique.auth]
# Azure App Gateway injects Entra ID principal
mode = "headers"
header_name = "X-Forwarded-User"
trusted_proxies = ["10.0.0.0/24"] # Application Gateway subnet

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

[resilience.sftp]
# Azure Blob Storage SFTP endpoint
enabled = true
host = "nieblabackups.blob.core.windows.net"
user = "nieblabackups.backupuser"
remote_path = "/backups/monorepo"
key_file = "/etc/niebla/ssh/azure_backup_id_ed25519"
timeout_seconds = 120

[resilience.schedule]
auto_backup = true
interval_hours = 4
retention_count = 30
snapshot_on_submit = true
```

---

## 4. Cloud Comparison Matrix

| Feature | Amazon Web Services (AWS) | Google Cloud Platform (GCP) | Microsoft Azure |
| :--- | :--- | :--- | :--- |
| **Recommended VM** | `c6i.2xlarge` (8 vCPU, 16 GB) | `n2-standard-8` (8 vCPU, 32 GB) | `Standard_D8s_v5` (8 vCPU, 32 GB) |
| **CAS Disk Type** | EBS GP3 (1 TB, 3,000 IOPS) | Extreme PD or Local NVMe SSD | Premium SSD v2 (1 TB, 3,000 IOPS) |
| **Managed Database** | Amazon RDS PostgreSQL (Multi-AZ) | Cloud SQL PostgreSQL (Regional HA) | Azure Database PostgreSQL Flexible |
| **Cotejo SSO Gateway** | ALB + Cognito / OIDC | Cloud Load Balancer + IAP | Application Gateway + Entra ID |
| **Injected Header** | `X-Amzn-Oidc-Identity` | `X-Goog-Authenticated-User-Email` | `X-Forwarded-User` |
| **Resilience Target** | Amazon S3 Standard / Glacier | Google Cloud Storage (S3 API) | Azure Blob Storage (SFTP / S3 MinIO) |

