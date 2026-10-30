package grpc

import (
	"context"
	"testing"

	nieblav1 "niebla/pkg/proto/nieblav1"
)

func TestShelveCL_AutoAssignReviewAgents(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, _, cleanup := setupTestEnvironment(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Establish base trunk CL with .niebla/agents.toml
	baseTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
		".niebla/agents.toml": `
[[agent]]
name = "sec-bot"
display_name = "Security Reviewer"
trigger_paths = ["pkg/security/**"]

[[agent]]
name = "proto-bot"
display_name = "Protobuf Linter"
trigger_paths = ["*.proto"]

[[agent]]
name = "global-bot"
display_name = "Global Inspector"
# Empty trigger_paths -> runs on all changes
`,
		"README.md": "# Base Monorepo\n",
	})

	baseResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "admin",
		Description:  "feat: initial base setup",
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

	// 2. Shelve CL modifying pkg/security/auth.go
	secTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
		".niebla/agents.toml": `
[[agent]]
name = "sec-bot"
display_name = "Security Reviewer"
trigger_paths = ["pkg/security/**"]

[[agent]]
name = "proto-bot"
display_name = "Protobuf Linter"
trigger_paths = ["*.proto"]

[[agent]]
name = "global-bot"
display_name = "Global Inspector"
`,
		"pkg/security/auth.go": "package security\n",
	})

	secResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "dev",
		Description:  "feat: add auth module",
		ParentCl:     baseResp.ClId,
		RootTreeHash: secTree,
	})
	if err != nil {
		t.Fatalf("failed to shelve sec CL: %v", err)
	}

	// Verify assigned reviewers for sec CL
	secReviewers, err := database.ListReviewers(ctx, secResp.ClId)
	if err != nil {
		t.Fatalf("failed to get reviewers for sec CL: %v", err)
	}

	revMap := make(map[string]bool)
	for _, r := range secReviewers {
		revMap[r.Reviewer] = true
	}

	if !revMap["sec-bot"] {
		t.Errorf("expected 'sec-bot' to be assigned to sec CL")
	}
	if !revMap["global-bot"] {
		t.Errorf("expected 'global-bot' to be assigned to sec CL")
	}
	if revMap["proto-bot"] {
		t.Errorf("expected 'proto-bot' NOT to be assigned to sec CL")
	}

	// 3. Shelve CL modifying api/service.proto
	protoTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
		".niebla/agents.toml": `
[[agent]]
name = "sec-bot"
display_name = "Security Reviewer"
trigger_paths = ["pkg/security/**"]

[[agent]]
name = "proto-bot"
display_name = "Protobuf Linter"
trigger_paths = ["*.proto"]

[[agent]]
name = "global-bot"
display_name = "Global Inspector"
`,
		"api/service.proto": "syntax = \"proto3\";\n",
	})

	protoResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "dev",
		Description:  "feat: add proto service",
		ParentCl:     baseResp.ClId,
		RootTreeHash: protoTree,
	})
	if err != nil {
		t.Fatalf("failed to shelve proto CL: %v", err)
	}

	protoReviewers, err := database.ListReviewers(ctx, protoResp.ClId)
	if err != nil {
		t.Fatalf("failed to get reviewers for proto CL: %v", err)
	}

	protoMap := make(map[string]bool)
	for _, r := range protoReviewers {
		protoMap[r.Reviewer] = true
	}

	if !protoMap["proto-bot"] {
		t.Errorf("expected 'proto-bot' to be assigned to proto CL")
	}
	if !protoMap["global-bot"] {
		t.Errorf("expected 'global-bot' to be assigned to proto CL")
	}
	if protoMap["sec-bot"] {
		t.Errorf("expected 'sec-bot' NOT to be assigned to proto CL")
	}
}
