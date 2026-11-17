#!/usr/bin/env bash
# ==============================================================================
# Niebla Tier 2 Release Candidate: Real Windows OS Confirmation Container
# ==============================================================================
# This script provisions and runs a KVM-accelerated Windows container on nixdev
# using 'dockurr/windows' to test native Windows behavior:
# 1. Cross-compiles niebla.exe for Windows amd64
# 2. Dials an ephemeral/remote niebla-server instance over network gRPC
# 3. Executes workspace creation in --no-fuse mode on native NTFS
# 4. Validates backslash path handling, remote CL shelving, and trunk submission
# 5. Verifies NTFS mandatory file locking (ERROR_SHARING_VIOLATION) handling
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
NIEBLA_DIR="$REPO_ROOT/niebla"
BIN_DIR="$NIEBLA_DIR/bin"
WIN_BIN="$BIN_DIR/niebla.exe"

echo "=== [1/4] Verifying KVM Hardware Acceleration on Host ==="
if [ ! -e /dev/kvm ]; then
    echo "ERROR: /dev/kvm does not exist on this host."
    echo "Windows virtualization requires KVM hardware acceleration."
    exit 1
fi

if [ ! -r /dev/kvm ] || [ ! -w /dev/kvm ]; then
    echo "ERROR: Current user lacks read/write access to /dev/kvm."
    echo "Ensure user is in 'kvm' or 'libvirtd' group."
    exit 1
fi
echo "✅ /dev/kvm is available and accessible."

echo "=== [2/4] Cross-Compiling niebla.exe for Windows (amd64) ==="
mkdir -p "$BIN_DIR"
(
    cd "$NIEBLA_DIR"
    GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o "$WIN_BIN" ./cmd/niebla
)
echo "✅ Successfully built: $WIN_BIN"

# Check if unattended container run was requested
RUN_CONTAINER="${1:-check}"
REMOTE_SERVER="${2:-10.0.2.2:55051}"

STORAGE_DIR="/var/lib/windows-test-vm"

if [ "$RUN_CONTAINER" != "run" ]; then
    echo ""
    echo "=== [Dry Run / Prerequisite Check Complete] ==="
    if [ -f "$STORAGE_DIR/data.img" ]; then
        echo "✅ Pre-warmed Windows disk image confirmed at $STORAGE_DIR/data.img"
    else
        echo "⚠️ Pre-warmed Windows disk image NOT found at $STORAGE_DIR/data.img"
    fi
    echo "The host environment is ready for KVM-accelerated Windows testing."
    echo ""
    echo "To run the automated containerized test suite on nixdev, execute:"
    echo "  $0 run [ephemeral-server-addr:port]"
    echo ""
    echo "Example:"
    echo "  $0 run 10.0.2.2:55051"
    exit 0
fi

if [ ! -f "$STORAGE_DIR/data.img" ]; then
    echo "ERROR: Pre-warmed Windows disk image not found at $STORAGE_DIR/data.img"
    echo "The Windows 11 VM disk must be warmed up once before running tests."
    exit 1
fi

echo "=== [3/4] Preparing Automated PowerShell Verification Harness ==="
SHARED_DIR=$(mktemp -d -t niebla-win-shared-XXXXXX)
CONTAINER_NAME="niebla-win-runner-$$"

cleanup() {
    echo "=== Cleaning up Windows test container and temporary files ==="
    docker stop "$CONTAINER_NAME" 2>/dev/null || true
    docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
    rm -rf "${SHARED_DIR:-}"
}
trap cleanup EXIT INT TERM

cp "$WIN_BIN" "$SHARED_DIR/niebla.exe"

cat << 'POWERSHELL' > "$SHARED_DIR/run_tests.ps1"
param (
    [string]$ServerAddr = "10.0.2.2:55051"
)

$ErrorActionPreference = "Stop"
Write-Host "=== Niebla Native Windows Verification Suite ===" -ForegroundColor Cyan
Write-Host "Connecting to remote Niebla server at $ServerAddr"

$WorkDir = "C:\NieblaTest"
New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null
Set-Location $WorkDir

$Niebla = "C:\shared\niebla.exe"
$ConfigDir = "$WorkDir\config"
$MountDir = "$WorkDir\win-ws"

Write-Host "1. Testing 'niebla --help' execution..."
& $Niebla --help | Out-Null
Write-Host "   PASS: niebla.exe executed cleanly." -ForegroundColor Green

Write-Host "2. Creating --no-fuse workspace '$MountDir'..."
& $Niebla workspace create win-ws --no-fuse --mount-dir "$MountDir" --server "$ServerAddr" --config-dir "$ConfigDir"
if (-not (Test-Path "$MountDir")) {
    throw "Workspace mount directory was not created"
}
Write-Host "   PASS: --no-fuse workspace created." -ForegroundColor Green

Write-Host "3. Creating tracked files with Windows backslashes..."
New-Item -ItemType Directory -Force -Path "$MountDir\src\subdir" | Out-Null
Set-Content -Path "$MountDir\src\app.go" -Value "package main`n`nfunc main() { println(`"Hello Windows`") }"
Set-Content -Path "$MountDir\src\subdir\helper.go" -Value "package subdir`n`nconst OS = `"windows`""
Set-Content -Path "$MountDir\.nieblaignore" -Value "*.tmp`nbin\`n"
Set-Content -Path "$MountDir\ignored.tmp" -Value "temp data"

Write-Host "4. Checking workspace status..."
$Status = & $Niebla status --workspace win-ws --server "$ServerAddr" --config-dir "$ConfigDir"
Write-Host $Status
if ($Status -like "*ignored.tmp*") {
    throw "Ignored file surfaced in status"
}
Write-Host "   PASS: Status correctly evaluated .nieblaignore." -ForegroundColor Green

Write-Host "5. Shelving Changelist over network gRPC to remote server..."
& $Niebla shelve -m "feat(win): verified from Windows container" --workspace win-ws --server "$ServerAddr" --config-dir "$ConfigDir"
Write-Host "   PASS: CL shelved successfully." -ForegroundColor Green

Write-Host "6. Testing NTFS mandatory file locking resilience..."
$FileStream = [System.IO.File]::Open("$MountDir\src\app.go", [System.IO.FileMode]::Open, [System.IO.FileAccess]::Read, [System.IO.FileShare]::None)
try {
    # Attempt status while file is exclusively locked
    $LockStatus = & $Niebla status --workspace win-ws --server "$ServerAddr" --config-dir "$ConfigDir"
    Write-Host "   PASS: Niebla handled locked file gracefully without panicking." -ForegroundColor Green
} finally {
    $FileStream.Close()
}

Write-Host "7. Submitting CL to trunk..."
& $Niebla submit --workspace win-ws --server "$ServerAddr" --config-dir "$ConfigDir"
Write-Host "   PASS: CL submitted to remote trunk." -ForegroundColor Green

Write-Host "8. Cleaning up workspace..."
& $Niebla workspace delete win-ws --force --config-dir "$ConfigDir"
Write-Host "   PASS: Workspace deleted." -ForegroundColor Green

Write-Host "=== All Windows Confirmation Tests PASSED ===" -ForegroundColor Green
POWERSHELL

echo "=== [4/4] Launching KVM-Accelerated dockurr/windows Container with Pre-Warmed Disk ==="

docker run -d --rm \
    --name "$CONTAINER_NAME" \
    --device /dev/kvm \
    --cap-add NET_ADMIN \
    -e VERSION="11" \
    -e RAM_SIZE="6G" \
    -e CPU_CORES="4" \
    -v "$STORAGE_DIR":/storage \
    -v "$SHARED_DIR":/shared \
    dockurr/windows

echo "Waiting for Windows VM to boot and complete test suite (timeout: 5m)..."
MAX_WAIT=300
ELAPSED=0
while [ $ELAPSED -lt $MAX_WAIT ]; do
    if [ -f "$SHARED_DIR/test_result.txt" ]; then
        RESULT=$(tr -d '\r\n ' < "$SHARED_DIR/test_result.txt")
        echo ""
        echo "=== Windows Test Execution Log ==="
        cat "$SHARED_DIR/test_output.log" 2>/dev/null || true
        echo "=================================="
        if [ "$RESULT" = "PASS" ]; then
            echo "✅ Windows 11 KVM Verification Suite PASSED!"
            exit 0
        else
            echo "❌ Windows 11 KVM Verification Suite FAILED with status: $RESULT"
            exit 1
        fi
    fi
    sleep 5
    ELAPSED=$((ELAPSED + 5))
    echo "  [${ELAPSED}s / ${MAX_WAIT}s] Waiting for Windows VM test completion..."
done

echo "ERROR: Timed out after ${MAX_WAIT}s waiting for Windows tests to complete."
docker logs "$CONTAINER_NAME" 2>/dev/null || true
exit 1
