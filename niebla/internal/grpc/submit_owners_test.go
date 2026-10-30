package grpc

import (
	"context"
	"testing"

	"niebla/internal/config"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSubmitCL_OwnersGating(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, server, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Configure static group resolution
	cfg := config.DefaultConfig()
	cfg.Critique.Groups = config.CritiqueGroupConfig{
		Mode: "static",
		Static: map[string][]string{
			"backend-leads": {"alice", "bob"},
		},
	}
	server.SetToolchainConfig(cfg)

	ctx := context.Background()

	// 1. Create a tree with root OWNERS.toml requiring "@backend-leads"
	rootTree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"OWNERS.toml": `
approvers = ["@backend-leads"]
`,
		"server.go": "package main\n",
	})

	shelveResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "dave",
		Description:  "initial server",
		RootTreeHash: rootTree,
	})
	if err != nil {
		t.Fatalf("failed to shelve CL: %v", err)
	}
	clID := shelveResp.ClId

	// 2. Submit without any approvals -> blocked by OWNERS policy!
	_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID})
	if err == nil {
		t.Fatalf("expected SubmitCL to fail due to missing OWNERS approval, got success")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got: %v", err)
	}

	// 3. Submit with non-owner approval ("charlie") -> still blocked
	_ = database.SetApproval(ctx, clID, "charlie", "lgtm")
	_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID})
	if err == nil {
		t.Fatalf("expected SubmitCL to fail with non-owner approval, got success")
	}

	// 4. Submit with Force = true -> bypasses OWNERS policy!
	forceResp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{
		ClId:  clID,
		Force: true,
	})
	if err != nil {
		t.Fatalf("expected SubmitCL with Force=true to succeed, got: %v", err)
	}
	if forceResp.ClId != clID {
		t.Errorf("expected CL ID %d, got %d", clID, forceResp.ClId)
	}

	// 5. Test legitimate owner approval: create second CL
	cl2Tree := putBlobAndTree(t, ctx, database, casStorage, map[string]string{
		"OWNERS.toml": `
approvers = ["@backend-leads"]
`,
		"server.go": "package main\n// updated\n",
	})
	shelveResp2, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "dave",
		Description:  "server update",
		ParentCl:     clID,
		RootTreeHash: cl2Tree,
	})
	if err != nil {
		t.Fatalf("failed to shelve CL 2: %v", err)
	}
	cl2ID := shelveResp2.ClId

	// Alice (member of @backend-leads) grants lgtm approval
	if err := database.SetApproval(ctx, cl2ID, "alice", "lgtm"); err != nil {
		t.Fatalf("failed to record approval: %v", err)
	}

	// Submit without Force -> succeeds because Alice is an authorized owner!
	submitResp2, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{
		ClId:  cl2ID,
		Force: false,
	})
	if err != nil {
		t.Fatalf("expected SubmitCL to succeed with authorized owner approval, got: %v", err)
	}
	if submitResp2.ClId != cl2ID {
		t.Errorf("expected CL ID %d, got %d", cl2ID, submitResp2.ClId)
	}
}
