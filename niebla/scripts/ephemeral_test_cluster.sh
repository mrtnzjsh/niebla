#!/usr/bin/env bash
# ==============================================================================
# Niebla Hermetic Test Cluster: 100% Isolated Containerized Environment
# ==============================================================================
# Provisions and tears down an isolated testing cluster with zero host dependencies,
# zero local filesystem modifications, and zero contact with production resources:
# 1. Creates a private Docker bridge network (niebla-test-net)
# 2. Spawns an ephemeral PostgreSQL container (niebla-test-postgres) on the test network
# 3. Compiles static niebla-server binary (CGO_ENABLED=0) into ephemeral /tmp build context
# 4. Streams build context over Docker socket to build ephemeral image (niebla-test-server:ephemeral)
# 5. Spawns ephemeral niebla-server container with a dedicated Docker volume for CAS
# 6. Tears down all containers, volumes, networks, and images cleanly on stop
# ==============================================================================

set -euo pipefail

ACTION="${1:-status}"
PORT="${2:-55051}"

NETWORK_NAME="niebla-test-net"
PG_CONTAINER="niebla-test-postgres"
SERVER_CONTAINER="niebla-test-server"
SERVER_IMAGE="niebla-test-server:ephemeral"
CAS_VOLUME="niebla-test-cas-vol"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

cleanup() {
    echo "=== Tearing down hermetic ephemeral test cluster ==="
    docker stop "$SERVER_CONTAINER" "$PG_CONTAINER" 2>/dev/null || true
    docker rm -f "$SERVER_CONTAINER" "$PG_CONTAINER" 2>/dev/null || true
    docker volume rm -f "$CAS_VOLUME" 2>/dev/null || true
    docker network rm "$NETWORK_NAME" 2>/dev/null || true
    docker rmi -f "$SERVER_IMAGE" 2>/dev/null || true
    echo "✅ Dedicated ephemeral test cluster destroyed completely."
}

start_cluster() {
    echo "=== Starting 100% isolated ephemeral test cluster ==="
    cleanup >/dev/null 2>&1 || true

    # 1. Create isolated Docker bridge network
    echo "1. Creating dedicated Docker network ($NETWORK_NAME)..."
    docker network create "$NETWORK_NAME"

    # 2. Start dedicated ephemeral PostgreSQL container
    echo "2. Spawning dedicated ephemeral PostgreSQL container..."
    docker run -d --rm \
        --name "$PG_CONTAINER" \
        --network "$NETWORK_NAME" \
        -e POSTGRES_USER=niebla \
        -e POSTGRES_PASSWORD=niebla \
        -e POSTGRES_DB=niebla_test \
        postgres:16-alpine

    echo "Waiting for ephemeral PostgreSQL to accept connections..."
    for i in {1..30}; do
        if docker exec "$PG_CONTAINER" pg_isready -U niebla >/dev/null 2>&1; then
            echo "✅ Ephemeral PostgreSQL container ready"
            break
        fi
        sleep 1
    done

    # 3. Compile static niebla-server and build ephemeral Docker image via stream
    echo "3. Compiling static niebla-server and building ephemeral container image..."
    TMP_BUILD_DIR=$(mktemp -d /tmp/niebla-test-build-XXXXXX)
    trap 'rm -rf "$TMP_BUILD_DIR"' EXIT

    CGO_ENABLED=0 go -C "$REPO_ROOT/niebla" build -o "$TMP_BUILD_DIR/niebla-server" ./cmd/niebla-server

    cat << 'DOCKERFILE' > "$TMP_BUILD_DIR/Dockerfile"
FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY niebla-server /usr/local/bin/niebla-server
VOLUME /cas
EXPOSE 55051
ENTRYPOINT ["/usr/local/bin/niebla-server"]
DOCKERFILE

    tar -czf - -C "$TMP_BUILD_DIR" . | docker build -t "$SERVER_IMAGE" -
    rm -rf "$TMP_BUILD_DIR"
    trap - EXIT

    # 4. Create dedicated Docker volume for CAS
    docker volume create "$CAS_VOLUME" >/dev/null

    # 5. Launch ephemeral niebla-server container
    echo "4. Launching ephemeral niebla-server container on port $PORT..."
    docker run -d --rm \
        --name "$SERVER_CONTAINER" \
        --network "$NETWORK_NAME" \
        -p "0.0.0.0:${PORT}:55051" \
        -v "$CAS_VOLUME":/cas \
        "$SERVER_IMAGE" \
            --host 0.0.0.0 \
            --port 55051 \
            --blob-path /cas \
            --auto-db=false \
            --db-url "postgres://niebla:niebla@${PG_CONTAINER}:5432/niebla_test?sslmode=disable"

    echo "Waiting for ephemeral niebla-server on port $PORT..."
    for i in {1..30}; do
        if nc -z 127.0.0.1 "$PORT" 2>/dev/null; then
            echo "✅ Ephemeral niebla-server listening on port $PORT"
            return 0
        fi
        sleep 1
    done

    echo "ERROR: Ephemeral niebla-server failed to start on port $PORT"
    docker logs "$SERVER_CONTAINER" 2>/dev/null || true
    cleanup
    return 1
}

case "$ACTION" in
    start)
        start_cluster
        ;;
    stop)
        cleanup
        ;;
    status)
        if docker ps --filter "name=$SERVER_CONTAINER" --format '{{.Names}}' | grep -q "$SERVER_CONTAINER"; then
            echo "✅ Ephemeral test cluster is RUNNING (server: $SERVER_CONTAINER, db: $PG_CONTAINER, net: $NETWORK_NAME)"
        else
            echo "Ephemeral test cluster is STOPPED."
        fi
        ;;
    *)
        echo "Usage: $0 {start|stop|status} [port]"
        exit 1
        ;;
esac
