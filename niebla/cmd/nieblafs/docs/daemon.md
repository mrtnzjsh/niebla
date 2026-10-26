# NieblaFS FUSE Daemon Documentation

## Overview

NieblaFS is the virtual FUSE workspace daemon for Niebla. It handles file operations such as lookup, readdir, open, read, write, and create, redirecting mutations to copy-on-write scratch overlays and synchronizing with `niebla-server`.

## Features

- **FUSE File System Integration**: Virtual directory hierarchy mounting under `/niebla/workspaces/<name>`.
- **gRPC Integration**: Communicates with `niebla-server` via `NieblaService`.
- **Copy-on-Write Scratch Overlays**: Isolates mutations under `/scratch/niebla/workspaces/<name>`.
- **Unit and Integration Tests**: Ensures daemon stability and correctness.

## Build and Run

To build the NieblaFS daemon binary:
```sh
go build -o bin/nieblafs ./cmd/nieblafs
```

To run NieblaFS:
```sh
./bin/nieblafs --mount /niebla/workspaces/ws1 --scratch /scratch/niebla/workspaces/ws1 --server 127.0.0.1:50051
```