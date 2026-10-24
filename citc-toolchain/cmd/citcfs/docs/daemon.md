# FUSE Daemon Documentation

## Overview

The FUSE Daemon is a lightweight file system daemon implemented in Go using the `bazil/fuse` library. It integrates with a gRPC server to handle file operations such as lookup, readdir, open, read, write, and create. The daemon uses a metadata cache to improve performance and reduce the number of calls to the gRPC server.

## Features

- **FUSE File System Integration**: Implements a basic FUSE file system with placeholder methods.
- **gRPC Client and Server**: Provides communication between the FUSE daemon and a remote server.
- **Metadata Cache**: Stores file metadata locally to speed up common operations.
- **Unit Tests**: Ensures the correctness of individual components.
- **Integration Tests**: Verifies the interaction between different parts of the system.
- **Performance Tests**: Measures the daemon's ability to handle typical loads.

## Setup and Initialization

### Project Initialization

1. **Create a new Go project**:
   ```sh
   mkdir citc-toolchain
   cd citc-toolchain
   ```

2. **Set up the project structure**:
   ```sh
   mkdir -p cmd/citcfs/internal/fs cmd/citcfs/internal/metadata cmd/citcfs/test cmd/citcfs/docs
   ```

3. **Initialize the Go module**:
   ```sh
   go mod init citc-toolchain
   ```

4. **Add dependencies**:
   ```sh
   go get github.com/bazil/fuse
   go get google.golang.org/grpc
   go get github.com/stretchr/testify
   ```

### Basic FUSE Daemon Structure

The main entry point for the FUSE daemon is located in `cmd/citcfs/main.go`. It initializes the FUSE mount and starts the server.

### Tests for Initialization

Unit tests for the initialization of the FUSE daemon are located in `cmd/citcfs/main_test.go`.

## gRPC Client and Server Placeholders

### gRPC Client

The gRPC client is defined in `cmd/citcfs/grpc_client.go`. It includes placeholder methods for handling file operations.

### gRPC Server Placeholder

The gRPC server is defined in `cmd/citcfs/grpc_server.go`. It includes placeholder methods for handling file operations and is configured to accept calls from within the local network.

### Tests for gRPC Client and Server

Unit tests for the gRPC client and server are located in `cmd/citcfs/test/grpc_test.go`.

## Metadata Cache and gRPC Integration

### Metadata Cache

The metadata cache is implemented in `cmd/citcfs/internal/metadata/cache.go`. It stores file metadata in an in-memory data structure.

### gRPC Integration for Lookup and Readdir

The lookup and readdir methods are implemented in `cmd/citcfs/internal/fs/root.go`. They use the metadata cache and call the gRPC server if necessary.

### Tests for Metadata Cache and gRPC Integration

Unit tests for the metadata cache and gRPC integration are located in `cmd/citcfs/test/metadata_cache_test.go`.

## Open and Read Operations

### Open Operation

The open operation is implemented in `cmd/citcfs/internal/fs/file.go`. It checks the local cache and calls the gRPC server if necessary.

### Read Operation

The read operation is implemented in `cmd/citcfs/internal/fs/file.go`. It serves content from the local cache or calls the gRPC server if necessary.

### Tests for Open and Read

Unit tests for the open and read operations are located in `cmd/citcfs/test/open_read_test.go`.

## Write and Create Operations

### Write Operation

The write operation is implemented in `cmd/citcfs/internal/fs/write.go`. It redirects file mutations to an overlay scratch directory and notifies the gRPC server.

### Create Operation

The create operation is implemented in `cmd/citcfs/internal/fs/create.go`. It creates new files in the overlay scratch directory and notifies the gRPC server.

### Tests for Write and Create

Unit tests for the write and create operations are located in `cmd/citcfs/test/write_create_test.go`.

## Integration and Testing

### Integration Testing

Integration tests are located in `cmd/citcfs/test/integration_test.go`. They test the entire FUSE daemon setup.

### Performance Testing

Performance tests are located in `cmd/citcfs/test/performance_test.go`. They measure the daemon's ability to handle typical loads.

### Documentation

This documentation is located in `cmd/citcfs/docs/daemon.md`. It provides an overview, features, setup instructions, and test details.

## Usage

To run the FUSE daemon, use the following command:
```sh
go run cmd/citcfs/main.go
```

The daemon will mount the FUSE file system at `/mnt/fuse`.

## Configuration

The daemon can be configured using environment variables and command-line flags. For example:
```sh
go run cmd/citcfs/main.go -port=8080
```

This command sets the gRPC server port to 8080.