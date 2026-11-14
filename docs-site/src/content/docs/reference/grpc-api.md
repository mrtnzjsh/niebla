---
title: "gRPC Service & Wire Protocol Reference"
description: Complete architectural specification of the NieblaService Protocol Buffers (protobuf) interface, streaming semantics, metadata authentication headers, and wire status codes.
sidebar:
  order: 4
  label: gRPC Service & Wire API
---

All communication between developer clients, the `nieblafs` virtual file system daemon, CI runner agents, and `niebla-server` is powered by high-performance **HTTP/2 gRPC** defined under `niebla.v1.NieblaService`.

This page provides the complete technical specification of the RPC methods, streaming protocols, message schemas, and authentication headers.

---

## 1. Service Definition & Package

```protobuf
syntax = "proto3";

package niebla.v1;

option go_package = "niebla/pkg/proto/nieblav1;nieblav1";

service NieblaService {
  // Tree & Metadata Lookups (VFS lookup, readdir, getattr)
  rpc GetTree (GetTreeRequest) returns (GetTreeResponse);
  rpc GetNode (GetNodeRequest) returns (NodeEntry);

  // Content Operations (VFS open/read & CLI shelve/diff)
  rpc GetBlob (GetBlobRequest) returns (stream BlobChunk);
  rpc ReadBlob (ReadBlobRequest) returns (stream BlobChunk);
  rpc UploadBlob (stream UploadBlobRequest) returns (UploadBlobResponse);

  // Workspace & Changelist Lifecycles
  rpc CreateWorkspace (CreateWorkspaceRequest) returns (CreateWorkspaceResponse);
  rpc ShelveCL (ShelveCLRequest) returns (ShelveCLResponse);
  rpc SubmitCL (SubmitCLRequest) returns (SubmitCLResponse);
  rpc GetHeadCL (GetHeadCLRequest) returns (GetHeadCLResponse);
  rpc GetCL (GetCLRequest) returns (GetCLResponse);
  rpc SyncCL (SyncCLRequest) returns (SyncCLResponse);
  rpc SyncWorkspace (SyncWorkspaceRequest) returns (SyncWorkspaceResponse);
}
```

---

## 2. Authentication Metadata Headers

Every RPC must present authentication metadata unless running in permissive local development mode. Credentials are sent using HTTP/2 metadata headers:

| Header Name | Format | Description |
| :--- | :--- | :--- |
| `authorization` | `Bearer <token>` | Standard RFC 6750 bearer token authorization. |
| `x-niebla-token` | `<token>` | Alternative header for environments where proxies strip standard `authorization` headers. |

### Go Client Authentication Example
```go
ctx := metadata.AppendToOutgoingContext(
    context.Background(),
    "authorization", "Bearer "+os.Getenv("NIEBLA_TOKEN"),
)
client.GetTree(ctx, &nieblav1.GetTreeRequest{TreeHash: hash})
```

---

## 3. Metadata & Tree Operations

### `GetTree`
Traverses a directory's Merkle tree manifest with paged streaming.

```protobuf
message GetTreeRequest {
  bytes tree_hash = 1;        // 32-byte SHA-256 Merkle root of the directory
  int32 page_size = 2;        // Default: 5000 entries per page
  string page_token = 3;      // Next page cursor
}

message GetTreeResponse {
  repeated NodeEntry entries = 1;
  string next_page_token = 2;
}

message NodeEntry {
  string name = 1;            // Entry filename or directory name
  uint32 mode = 2;            // POSIX permissions (e.g. 0100644, 0100755, 0040755)
  uint64 size = 3;            // Uncompressed size in bytes
  bytes blob_sha256 = 4;      // 32-byte SHA-256 hash (nil for directories)
  bytes sub_tree_hash = 5;    // 32-byte SHA-256 Merkle hash (nil for leaf files)
}
```

---

## 4. Content Streaming Operations

### `GetBlob` / `ReadBlob`
Streams blob content from Content-Addressable Storage (CAS) with optional byte range offsets.

```protobuf
message GetBlobRequest {
  bytes sha256 = 1;           // 32-byte SHA-256 digest of the desired blob
  uint64 offset = 2;          // Byte offset to start reading from
  uint64 length = 3;          // Number of bytes to read (0 = read until EOF)
}

message BlobChunk {
  bytes data = 1;             // Binary chunk slice (default: 64KB)
}
```

### `UploadBlob` (Client Streaming)
Streams raw file bytes to `niebla-server`'s temporary staging directory (`.tmp/`). As chunks stream in, the server computes the SHA-256 checksum on-the-fly and atomically commits the file to its hash-sharded path (`ab/cd/<hash>`) once closed.

```protobuf
message UploadBlobRequest {
  bytes data = 1;             // File chunk slice (typically 64KB chunks)
}

message UploadBlobResponse {
  bytes sha256 = 1;           // Verified 32-byte SHA-256 digest
  uint64 size = 2;            // Total bytes committed to storage
}
```

---

## 5. Workspace & Changelist Lifecycles

### `ShelveCL`
Uploads modified/added scratch files, computes recursive Merkle tree hashes, and records a changelist or new patchset.

```protobuf
message ShelveCLRequest {
  string workspace_id = 1;
  string author = 2;          // Author username or authenticated principal
  string description = 3;     // Changelist commit message
  bytes root_tree_hash = 4;   // 32-byte Merkle root computed by client
  int64 parent_cl = 5;        // Parent changelist (or 0 for trunk head)
  repeated string reviewers = 6;
  int64 cl_id = 7;            // Optional: specify existing CL ID to upload a new patchset
}

message ShelveCLResponse {
  int64 cl_id = 1;            // Assigned or updated Changelist ID
}
```

### `SubmitCL`
Atomically transitions a changelist to `submitted` status in a serialized PostgreSQL transaction. If parent CL lags behind trunk head, performs automated 3-way Merkle tree merge. Automatically cascades rebases across stacked child changelists.

```protobuf
message SubmitCLRequest {
  int64 cl_id = 1;            // Changelist ID to submit
  bool force = 2;             // Bypass non-blocking presubmit warnings
}

message SubmitCLResponse {
  int64 cl_id = 1;
  int64 committed_at_unix = 2; // Timestamp of trunk submission
  repeated int64 rebased_children = 3; // Child CLs automatically re-parented onto trunk
}
```

### `SyncWorkspace`
Reconciles client workspace metadata with upstream parent or trunk movements.

```protobuf
message SyncWorkspaceRequest {
  string workspace_id = 1;
  int64 cl_id = 2;            // Active changelist ID or 0 for base CL
}

message SyncWorkspaceResponse {
  string workspace_id = 1;
  int64 cl_id = 2;
  int64 parent_cl = 3;
  bytes root_tree_hash = 4;   // New base Merkle root tree
  bytes parent_root_tree_hash = 5;
  bool needs_rebase = 6;      // True if merge conflicts exist
  string rebase_conflict_details = 7;
  int32 patchset_number = 8;
}
```

---

## 6. Standard RPC Error Codes

| Status Code | Trigger Condition | Recommended Client Action |
| :--- | :--- | :--- |
| `OK` (0) | Success | Proceed. |
| `InvalidArgument` (3) | Missing `cl_id`, empty description, or corrupt hash | Validate inputs before making RPC. |
| `NotFound` (5) | Target CL, blob hash, or workspace does not exist | Verify CL ID or workspace registration. |
| `FailedPrecondition` (9) | Submission blocked by required approvals, failing check runs, or merge conflict | Run `niebla sync` or collect required code reviews. |
| `Unauthenticated` (16) | Missing or invalid bearer token | Set `NIEBLA_TOKEN` or authenticate with `niebla login`. |
| `PermissionDenied` (7) | Attempting to shelve/submit without subtree ownership rights | Obtain sign-off in `OWNERS.toml`. |
