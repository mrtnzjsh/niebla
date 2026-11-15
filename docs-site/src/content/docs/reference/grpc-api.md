---
title: "gRPC Wire API"
description: Specification of the NieblaService Protocol Buffers (protobuf) interface, streaming RPCs, authentication headers, and status codes.
sidebar:
  order: 4
---

All communication between the `niebla` CLI, the `nieblafs` [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) daemon, `cotejo`, and `niebla-server` uses HTTP/2 [gRPC](https://en.wikipedia.org/wiki/GRPC) and [Protocol Buffers](https://en.wikipedia.org/wiki/Protocol_Buffers) defined in `niebla.v1.NieblaService`.

---

## 1. Service Definition

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

When `server.auth_tokens` is configured on `niebla-server`, every unary and streaming RPC must include a bearer token in its HTTP/2 metadata:

| Header Name | Format | Description |
| :--- | :--- | :--- |
| `authorization` | `Bearer <token>` | Standard RFC 6750 bearer token header. |
| `x-niebla-token` | `<token>` | Fallback header for reverse proxies that strip `authorization`. |

```go
ctx := metadata.AppendToOutgoingContext(
    context.Background(),
    "authorization", "Bearer "+os.Getenv("NIEBLA_TOKEN"),
)
resp, err := client.GetTree(ctx, &nieblav1.GetTreeRequest{TreeHash: hash})
```

---

## 3. Metadata & Tree Messages

### `GetTree`

Returns paged directory entries for a 32-byte [SHA-256](https://en.wikipedia.org/wiki/SHA-2) [Merkle tree](https://en.wikipedia.org/wiki/Merkle_tree) digest.

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
  uint32 mode = 2;            // POSIX permissions (e.g., 0100644, 0100755, 0040755)
  uint64 size = 3;            // Uncompressed size in bytes
  bytes blob_sha256 = 4;      // 32-byte SHA-256 hash (empty for directories)
  bytes sub_tree_hash = 5;    // 32-byte SHA-256 Merkle hash (empty for files)
}
```

---

## 4. Content Streaming Messages

### `GetBlob` / `ReadBlob`

Streams file bytes from [Content-Addressable Storage (CAS)](https://en.wikipedia.org/wiki/Content-addressable_storage) with optional byte-range slicing (`offset` and `length`).

```protobuf
message GetBlobRequest {
  bytes sha256 = 1;           // 32-byte SHA-256 digest of the blob
  uint64 offset = 2;          // Starting byte offset
  uint64 length = 3;          // Bytes to read (0 = read until EOF)
}

message BlobChunk {
  bytes data = 1;             // Binary chunk slice (64 KB default)
}
```

### `UploadBlob`

Streams raw file chunks from the client into `niebla-server`'s staging directory (`.tmp/`). The server computes the SHA-256 digest on the fly and atomically renames the file to its sharded CAS path (`ab/cd/<sha256>`) when the stream closes.

```protobuf
message UploadBlobRequest {
  bytes data = 1;             // File chunk slice (64 KB default)
}

message UploadBlobResponse {
  bytes sha256 = 1;           // Verified 32-byte SHA-256 digest
  uint64 size = 2;            // Total bytes committed to CAS
}
```

---

## 5. Workspace & Changelist Messages

### `ShelveCL`

Records a new changelist or appends a new patchset ($N+1$) to an existing changelist.

```protobuf
message ShelveCLRequest {
  string workspace_id = 1;
  string author = 2;
  string description = 3;
  bytes root_tree_hash = 4;   // 32-byte Merkle root of the shelved tree
  int64 parent_cl = 5;        // Parent CL ID (0 for trunk head)
  repeated string reviewers = 6;
  int64 cl_id = 7;            // Optional: existing CL ID to update with a new patchset
}

message ShelveCLResponse {
  int64 cl_id = 1;
}
```

### `SubmitCL`

Atomically commits an approved changelist to the linear trunk inside a serialized PostgreSQL transaction (`pg_advisory_xact_lock`) and cascades rebases to stacked child CLs.

```protobuf
message SubmitCLRequest {
  int64 cl_id = 1;
  bool force = 2;             // Bypass non-blocking presubmit warnings
}

message SubmitCLResponse {
  int64 cl_id = 1;
  int64 committed_at_unix = 2;
  repeated int64 rebased_children = 3;
}
```

### `SyncWorkspace`

Reconciles a workspace's base tree with the latest trunk head or parent CL revision.

```protobuf
message SyncWorkspaceRequest {
  string workspace_id = 1;
  int64 cl_id = 2;
}

message SyncWorkspaceResponse {
  string workspace_id = 1;
  int64 cl_id = 2;
  int64 parent_cl = 3;
  bytes root_tree_hash = 4;
  bytes parent_root_tree_hash = 5;
  bool needs_rebase = 6;
  string rebase_conflict_details = 7;
  int32 patchset_number = 8;
}
```

---

## 6. gRPC Status Codes

| Status Code | Trigger Condition | Client Action |
| :--- | :--- | :--- |
| `OK` (`0`) | Request succeeded. | Proceed. |
| `InvalidArgument` (`3`) | Malformed SHA-256 hash, missing `cl_id`, or empty description. | Check request arguments. |
| `NotFound` (`5`) | Requested CL, workspace, or CAS blob hash does not exist. | Verify the CL ID or upload missing blobs first. |
| `PermissionDenied` (`7`) | Caller lacks required `OWNERS.toml` permissions. | Request approval from a subtree owner. |
| `FailedPrecondition` (`9`) | Submit blocked by missing approvals, unresolved threads, failing CI, or conflict. | Run `niebla sync` or resolve open review gates. |
| `Unauthenticated` (`16`) | Missing or invalid bearer token. | Set `NIEBLA_TOKEN` or `core.auth_token`. |
