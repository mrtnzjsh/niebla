package grpc

import (
	"context"
	"testing"

	"niebla/internal/config"
	"niebla/internal/db"
	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSubmitCL_CheckRunsGate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test in short mode")
	}

	server, database, casStorage, _, cleanup := setupEventsTestServer(t)
	defer cleanup()

	ctx := context.Background()

	// Configure toolchain config with RequireGreenCheckRuns = true
	trueVal := true
	cfg := &config.Config{
		Critique: config.CritiqueConfig{
			RequireGreenCheckRuns: &trueVal,
		},
	}
	server.SetToolchainConfig(cfg)

	// Create root tree for CL 2
	bHash, bSize := putTestBlob(t, casStorage, database, []byte("package main\nfunc main() {}\n"))
	rootTreeHash := putTestTree(t, database, []tree.Entry{
		{Name: "main.go", Mode: 0100644, Size: uint64(bSize), BlobSHA256: bHash},
	})

	shelveResp, err := server.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "ci_developer",
		Description:  "Feature with CI gate",
		RootTreeHash: rootTreeHash,
		ParentCl:     1,
	})
	if err != nil {
		t.Fatalf("ShelveCL: %v", err)
	}
	clID := shelveResp.ClId

	// 1. Attempt to submit with 0 check runs -> MUST fail
	_, err = server.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID})
	if err == nil {
		t.Fatalf("expected SubmitCL to fail when 0 check runs exist, but it succeeded")
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got code %v, err: %v", st.Code(), err)
	}

	// 2. Report check run as "running" -> MUST fail
	_, err = server.ReportCheckRun(ctx, db.CheckRun{
		CLID:           clID,
		PatchsetNumber: 1,
		RunnerName:     "local-sandbox",
		Provider:       "local",
		Status:         "running",
	})
	if err != nil {
		t.Fatalf("ReportCheckRun: %v", err)
	}

	_, err = server.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID})
	if err == nil {
		t.Fatalf("expected SubmitCL to fail when check run is running, but it succeeded")
	}
	st, ok = status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got code %v, err: %v", st.Code(), err)
	}

	// 3. Report check run as "failed" -> MUST fail
	_, err = server.ReportCheckRun(ctx, db.CheckRun{
		CLID:           clID,
		PatchsetNumber: 1,
		RunnerName:     "local-sandbox",
		Provider:       "local",
		Status:         "failed",
	})
	if err != nil {
		t.Fatalf("ReportCheckRun failed: %v", err)
	}

	_, err = server.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID})
	if err == nil {
		t.Fatalf("expected SubmitCL to fail when check run is failed, but it succeeded")
	}
	st, ok = status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		t.Fatalf("expected FailedPrecondition, got code %v, err: %v", st.Code(), err)
	}

	// 4. Force submit with failed checks -> MUST succeed
	forceResp, err := server.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID, Force: true})
	if err != nil {
		t.Fatalf("expected SubmitCL with Force=true to succeed, got: %v", err)
	}
	if forceResp.ClId != clID {
		t.Errorf("expected CL %d, got %d", clID, forceResp.ClId)
	}

	// 5. Test another CL that passes checks cleanly
	shelveResp2, err := server.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "ci_developer2",
		Description:  "Clean passing CI CL",
		RootTreeHash: rootTreeHash,
		ParentCl:     clID,
	})
	if err != nil {
		t.Fatalf("ShelveCL 2: %v", err)
	}
	clID2 := shelveResp2.ClId

	// Report passed check run
	_, err = server.ReportCheckRun(ctx, db.CheckRun{
		CLID:           clID2,
		PatchsetNumber: 1,
		RunnerName:     "local-sandbox",
		Provider:       "local",
		Status:         "passed",
	})
	if err != nil {
		t.Fatalf("ReportCheckRun passed: %v", err)
	}

	// Submit without Force -> MUST succeed
	submitResp, err := server.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clID2})
	if err != nil {
		t.Fatalf("expected SubmitCL to succeed when checks are passed, got: %v", err)
	}
	if submitResp.ClId != clID2 {
		t.Errorf("expected CL %d, got %d", clID2, submitResp.ClId)
	}
}
