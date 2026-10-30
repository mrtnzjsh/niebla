package grpc

import (
	"context"
	"strings"
	"testing"

	nieblav1 "niebla/pkg/proto/nieblav1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSubmitCL_HooksGating(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Establish base trunk CL with .niebla/hooks.toml
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
name = "forbid-secrets"
type = "tree_scan"
blocking = true
description = "Block committed credentials"
[server.presubmit.tree_scan]
forbidden_patterns = ["*.pem", "*.key"]
`,
		"README.md": "# Sample Project\n",
	})

	baseResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "admin",
		Description:  "feat: initial project setup",
		RootTreeHash: baseTree,
	})
	if err != nil {
		t.Fatalf("failed to shelve base CL: %v", err)
	}

	// Submit base CL to establish trunk
	_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: baseResp.ClId, Force: true})
	if err != nil {
		t.Fatalf("failed to submit base CL: %v", err)
	}

	// 2. Test metadata hook: CL with malformed description is blocked
	badDescTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
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
`,
		"README.md": "# Sample Project (updated)\n",
	})

	badDescResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "dev",
		Description:  "wip: fix readme", // Invalid: does not start with feat/fix/docs
		ParentCl:     baseResp.ClId,
		RootTreeHash: badDescTree,
	})
	if err != nil {
		t.Fatalf("failed to shelve bad desc CL: %v", err)
	}

	_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: badDescResp.ClId})
	if err == nil {
		t.Fatalf("expected submit to be blocked by metadata hook, but got success")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition || !strings.Contains(st.Message(), "presubmit hook(s) failed") {
		t.Fatalf("expected FailedPrecondition with presubmit failure, got: %v", err)
	}

	// 3. Test tree_scan hook: CL adding forbidden *.pem file is blocked
	secretTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
		".niebla/hooks.toml": `
[[server.presubmit]]
name = "forbid-secrets"
type = "tree_scan"
blocking = true
description = "Block committed credentials"
[server.presubmit.tree_scan]
forbidden_patterns = ["*.pem", "*.key"]
`,
		"certs/server.pem": "CERTIFICATE DATA\n",
	})

	secretResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "dev",
		Description:  "feat: add server TLS cert",
		ParentCl:     baseResp.ClId,
		RootTreeHash: secretTree,
	})
	if err != nil {
		t.Fatalf("failed to shelve secret CL: %v", err)
	}

	_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: secretResp.ClId})
	if err == nil {
		t.Fatalf("expected submit to be blocked by tree_scan hook, but got success")
	}
	st, ok = status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition || !strings.Contains(st.Message(), "forbidden pattern") {
		t.Fatalf("expected FailedPrecondition with forbidden pattern, got: %v", err)
	}

	// 4. Test Force bypass: emergency submission bypasses presubmit hooks
	forceResp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{
		ClId:  secretResp.ClId,
		Force: true,
	})
	if err != nil {
		t.Fatalf("expected Force=true to bypass presubmit hooks, got: %v", err)
	}
	if forceResp.ClId != secretResp.ClId {
		t.Errorf("expected CL %d, got %d", secretResp.ClId, forceResp.ClId)
	}

	// 5. Test valid submission: clean description and clean files pass!
	validTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
		".niebla/hooks.toml": `
[[server.presubmit]]
name = "conventional-commits"
type = "metadata"
blocking = true
[server.presubmit.metadata]
field = "description"
pattern = "^(feat|fix|docs): .+"

[[server.presubmit]]
name = "forbid-secrets"
type = "tree_scan"
blocking = true
[server.presubmit.tree_scan]
forbidden_patterns = ["*.pem", "*.key"]
`,
		"src/feature.go": "package src\n",
	})

	validResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "dev",
		Description:  "feat: implement clean feature",
		ParentCl:     secretResp.ClId,
		RootTreeHash: validTree,
	})
	if err != nil {
		t.Fatalf("failed to shelve valid CL: %v", err)
	}

	validSubmitResp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: validResp.ClId})
	if err != nil {
		t.Fatalf("expected clean submission to succeed, got: %v", err)
	}
	if validSubmitResp.ClId != validResp.ClId {
		t.Errorf("expected CL %d, got %d", validResp.ClId, validSubmitResp.ClId)
	}
}
