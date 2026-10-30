package grpc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"niebla/pkg/cas"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHooksIntegration_FullPresubmitAndCASPipeline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Verify Direct CAS upload via gRPC UploadBlob (matching 'niebla cas put')
	t.Run("direct_cas_upload_blob", func(t *testing.T) {
		binaryContent := bytes.Repeat([]byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01}, 1024) // 7KB binary
		expectedSum := sha256.Sum256(binaryContent)
		expectedHex := hex.EncodeToString(expectedSum[:])

		stream, err := client.UploadBlob(ctx)
		if err != nil {
			t.Fatalf("failed to open UploadBlob stream: %v", err)
		}

		chunkSize := cas.DefaultChunkSize
		for offset := 0; offset < len(binaryContent); offset += chunkSize {
			end := offset + chunkSize
			if end > len(binaryContent) {
				end = len(binaryContent)
			}
			if err := stream.Send(&nieblav1.UploadBlobRequest{Data: binaryContent[offset:end]}); err != nil {
				t.Fatalf("failed to send chunk: %v", err)
			}
		}

		resp, err := stream.CloseAndRecv()
		if err != nil {
			t.Fatalf("failed to close UploadBlob stream: %v", err)
		}

		if hex.EncodeToString(resp.Sha256) != expectedHex {
			t.Errorf("expected SHA-256 %s, got %s", expectedHex, hex.EncodeToString(resp.Sha256))
		}
		if resp.Size != uint64(len(binaryContent)) {
			t.Errorf("expected size %d, got %d", len(binaryContent), resp.Size)
		}

		// Verify blob is registered in central PostgreSQL
		blobRecord, err := database.GetBlob(ctx, resp.Sha256)
		if err != nil || blobRecord == nil {
			t.Fatalf("blob not found in PostgreSQL blobs table: %v", err)
		}
		if blobRecord.ByteSize != int64(len(binaryContent)) {
			t.Errorf("db record size mismatch: %d != %d", blobRecord.ByteSize, len(binaryContent))
		}
	})

	// 2. Establish Base Trunk with .niebla/hooks.toml
	baseTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
		".niebla/hooks.toml": `
[[server.presubmit]]
name = "conventional-commits"
type = "metadata"
blocking = true
description = "Enforce Conventional Commits"
[server.presubmit.metadata]
field = "description"
pattern = "^(feat|fix|docs): .+"
min_length = 10

[[server.presubmit]]
name = "forbid-credentials"
type = "tree_scan"
blocking = true
description = "Forbid private keys and oversized files"
[server.presubmit.tree_scan]
forbidden_patterns = ["*.pem", "*.key", "id_rsa*"]
max_file_size_bytes = 102400 # 100 KB limit
`,
		"README.md": "# Production Monorepo\n",
	})

	baseCL, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "admin@corp.internal",
		Description:  "feat: initial monorepo configuration",
		RootTreeHash: baseTree,
	})
	if err != nil {
		t.Fatalf("failed to shelve base CL: %v", err)
	}

	submitBaseResp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: baseCL.ClId, Force: true})
	if err != nil {
		t.Fatalf("failed to submit base CL to trunk: %v", err)
	}
	headCLID := submitBaseResp.ClId

	// Case 1: Malformed description rejected by metadata hook
	t.Run("metadata_hook_rejects_malformed_description", func(t *testing.T) {
		clTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
			".niebla/hooks.toml": `
[[server.presubmit]]
name = "conventional-commits"
type = "metadata"
blocking = true
[server.presubmit.metadata]
field = "description"
pattern = "^(feat|fix|docs): .+"
`,
			"README.md": "# Production Monorepo (v2)\n",
		})

		badCL, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
			Author:       "dev@corp.internal",
			Description:  "random commit message",
			ParentCl:     headCLID,
			RootTreeHash: clTree,
		})
		if err != nil {
			t.Fatalf("failed to shelve CL: %v", err)
		}

		_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: badCL.ClId})
		if err == nil {
			t.Fatalf("expected SubmitCL to fail due to metadata hook, but got success")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.FailedPrecondition || !strings.Contains(st.Message(), "conventional-commits") {
			t.Fatalf("expected FailedPrecondition with conventional-commits error, got: %v", err)
		}
	})

	// Case 2: Forbidden credentials rejected by tree_scan hook
	t.Run("tree_scan_hook_rejects_forbidden_patterns", func(t *testing.T) {
		clTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
			".niebla/hooks.toml": `
[[server.presubmit]]
name = "forbid-credentials"
type = "tree_scan"
blocking = true
[server.presubmit.tree_scan]
forbidden_patterns = ["*.pem", "*.key"]
`,
			"config/ssl/private.key": "-----BEGIN PRIVATE KEY-----\nMIIEvg...\n",
		})

		secretCL, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
			Author:       "dev@corp.internal",
			Description:  "feat: add SSL configuration",
			ParentCl:     headCLID,
			RootTreeHash: clTree,
		})
		if err != nil {
			t.Fatalf("failed to shelve secret CL: %v", err)
		}

		_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: secretCL.ClId})
		if err == nil {
			t.Fatalf("expected SubmitCL to fail due to forbidden pattern, but got success")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.FailedPrecondition || !strings.Contains(st.Message(), "matches forbidden pattern") {
			t.Fatalf("expected FailedPrecondition with forbidden pattern error, got: %v", err)
		}
	})

	// Case 3: Oversized file rejected by tree_scan hook
	t.Run("tree_scan_hook_rejects_oversized_file", func(t *testing.T) {
		largeContent := string(bytes.Repeat([]byte("A"), 150*1024)) // 150KB > 100KB limit
		clTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
			".niebla/hooks.toml": `
[[server.presubmit]]
name = "forbid-credentials"
type = "tree_scan"
blocking = true
[server.presubmit.tree_scan]
max_file_size_bytes = 102400
`,
			"data/bigfile.txt": largeContent,
		})

		largeCL, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
			Author:       "dev@corp.internal",
			Description:  "feat: upload large dataset",
			ParentCl:     headCLID,
			RootTreeHash: clTree,
		})
		if err != nil {
			t.Fatalf("failed to shelve large CL: %v", err)
		}

		_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: largeCL.ClId})
		if err == nil {
			t.Fatalf("expected SubmitCL to fail due to file size, but got success")
		}
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.FailedPrecondition || !strings.Contains(st.Message(), "exceeds maximum permitted size") {
			t.Fatalf("expected FailedPrecondition with file size error, got: %v", err)
		}
	})

	// Case 4: Emergency Force bypass
	t.Run("force_bypass_submits_despite_failing_hooks", func(t *testing.T) {
		clTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
			".niebla/hooks.toml": `
[[server.presubmit]]
name = "conventional-commits"
type = "metadata"
blocking = true
[server.presubmit.metadata]
field = "description"
pattern = "^(feat|fix|docs): .+"
`,
			"hotfix.txt": "emergency patch\n",
		})

		hotfixCL, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
			Author:       "oncall@corp.internal",
			Description:  "EMERGENCY HOTFIX - NO CONVENTIONAL PREFIX",
			ParentCl:     headCLID,
			RootTreeHash: clTree,
		})
		if err != nil {
			t.Fatalf("failed to shelve hotfix CL: %v", err)
		}

		// Submit with Force = true
		forceResp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{
			ClId:  hotfixCL.ClId,
			Force: true,
		})
		if err != nil {
			t.Fatalf("expected Force=true to bypass presubmit hooks, got: %v", err)
		}
		if forceResp.ClId != hotfixCL.ClId {
			t.Errorf("expected CL ID %d, got %d", hotfixCL.ClId, forceResp.ClId)
		}
		headCLID = forceResp.ClId
	})

	// Case 5: Fully compliant change passes cleanly
	t.Run("clean_cl_passes_all_hooks_and_commits", func(t *testing.T) {
		clTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
			".niebla/hooks.toml": `
[[server.presubmit]]
name = "conventional-commits"
type = "metadata"
blocking = true
[server.presubmit.metadata]
field = "description"
pattern = "^(feat|fix|docs): .+"

[[server.presubmit]]
name = "forbid-credentials"
type = "tree_scan"
blocking = true
[server.presubmit.tree_scan]
forbidden_patterns = ["*.pem", "*.key"]
max_file_size_bytes = 102400
`,
			"src/auth.go": "package src\n// clean compliant code\n",
		})

		cleanCL, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
			Author:       "dev@corp.internal",
			Description:  "feat: implement clean auth handler",
			ParentCl:     headCLID,
			RootTreeHash: clTree,
		})
		if err != nil {
			t.Fatalf("failed to shelve clean CL: %v", err)
		}

		cleanResp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: cleanCL.ClId})
		if err != nil {
			t.Fatalf("expected compliant CL to submit successfully, got: %v", err)
		}
		if cleanResp.ClId != cleanCL.ClId {
			t.Errorf("expected CL %d, got %d", cleanCL.ClId, cleanResp.ClId)
		}
	})
}
