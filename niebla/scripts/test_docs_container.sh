#!/usr/bin/env bash
# ==============================================================================
# Niebla Hermetic Documentation Site Verification
# ==============================================================================
# Builds and audits the Astro/Starlight documentation site in an isolated
# node:22-alpine container with zero host dependencies and zero host pollution.
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DOCS_DIR="$REPO_ROOT/docs-site"
DOCS_VOL="niebla-docs-shared-$$"

cleanup() {
    docker volume rm -f "$DOCS_VOL" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "=== [1/2] Streaming docs-site into ephemeral Docker volume ($DOCS_VOL) ==="
docker volume create "$DOCS_VOL" >/dev/null
tar -C "$DOCS_DIR" -cf - . | docker run -i --rm -v "$DOCS_VOL":/app alpine:latest tar -C /app -xf -

echo "=== [2/2] Compiling and Auditing Docs in node:22-alpine ==="
docker run --rm \
    -v "$DOCS_VOL":/app \
    -w /app \
    node:22-alpine \
    sh -c "npm ci && npm run build && node scripts/verify-docs.cjs"

echo "✅ Containerized documentation verification completed successfully."
