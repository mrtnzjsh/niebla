//go:build rc

package rc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"testing"
	"time"

	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"
)

func TestMultiAgentConcurrencyStorm(t *testing.T) {
	cluster := SetupRCCluster(t)
	defer cluster.Cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	const numAgents = 15
	type agentResult struct {
		agentID       int
		clID          int64
		trunkRevision int64
		err           error
	}

	results := make([]agentResult, numAgents)
	var wg sync.WaitGroup

	t.Logf("🚀 Launching concurrency storm with %d parallel agents submitting to trunk...", numAgents)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentIdx int) {
			defer wg.Done()

			res := agentResult{agentID: agentIdx}

			// 1. Generate unique file content for this agent
			fileName := fmt.Sprintf("service_%02d.txt", agentIdx)
			content := []byte(fmt.Sprintf("Agent %02d payload generated at %s\n", agentIdx, time.Now().UTC().Format(time.RFC3339Nano)))
			fileHash := sha256.Sum256(content)

			// 2. Upload blob over streaming gRPC
			stream, err := cluster.GRPCClient.UploadBlob(ctx)
			if err != nil {
				res.err = fmt.Errorf("agent %d UploadBlob stream failed: %w", agentIdx, err)
				results[agentIdx] = res
				return
			}

			if err := stream.Send(&nieblav1.UploadBlobRequest{Data: content}); err != nil {
				res.err = fmt.Errorf("agent %d Send blob failed: %w", agentIdx, err)
				results[agentIdx] = res
				return
			}

			uploadResp, err := stream.CloseAndRecv()
			if err != nil {
				res.err = fmt.Errorf("agent %d CloseAndRecv failed: %w", agentIdx, err)
				results[agentIdx] = res
				return
			}
			if !bytes.Equal(uploadResp.Sha256, fileHash[:]) {
				res.err = fmt.Errorf("agent %d hash mismatch", agentIdx)
				results[agentIdx] = res
				return
			}

			// 3. Construct Merkle tree node
			entries := []tree.Entry{
				{
					Name:       fileName,
					Mode:       0100644,
					Size:       uint64(len(content)),
					BlobSHA256: fileHash[:],
				},
			}
			tree.SortEntries(entries)
			treeHash := tree.ComputeTreeHash(entries)

			// Persist tree nodes in DB
			if err := cluster.DB.InsertTreeNodes(ctx, treeHash, entries); err != nil {
				res.err = fmt.Errorf("agent %d InsertTreeNodes failed: %w", agentIdx, err)
				results[agentIdx] = res
				return
			}

			// 4. Shelve changelist
			shelveResp, err := cluster.GRPCClient.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
				Author:       fmt.Sprintf("agent-worker-%02d", agentIdx),
				Description:  fmt.Sprintf("feat: automated submission from agent %02d", agentIdx),
				RootTreeHash: treeHash,
				ParentCl:     0,
			})
			if err != nil {
				res.err = fmt.Errorf("agent %d ShelveCL failed: %w", agentIdx, err)
				results[agentIdx] = res
				return
			}

			res.clID = shelveResp.ClId

			// 5. Submit to trunk
			_, err = cluster.GRPCClient.SubmitCL(ctx, &nieblav1.SubmitCLRequest{
				ClId: shelveResp.ClId,
			})
			if err != nil {
				res.err = fmt.Errorf("agent %d SubmitCL failed: %w", agentIdx, err)
				results[agentIdx] = res
				return
			}

			// Query DB for assigned trunk revision
			clRecord, err := cluster.DB.GetCL(ctx, shelveResp.ClId)
			if err != nil {
				res.err = fmt.Errorf("agent %d GetCL failed: %w", agentIdx, err)
				results[agentIdx] = res
				return
			}
			if clRecord.TrunkRevision != nil {
				res.trunkRevision = *clRecord.TrunkRevision
			}
			results[agentIdx] = res
		}(i)
	}

	wg.Wait()

	// 6. Verify results
	revisionsSeen := make(map[int64]int)
	for _, res := range results {
		if res.err != nil {
			t.Fatalf("agent %d failed in concurrency storm: %v", res.agentID, res.err)
		}
		if res.trunkRevision <= 0 {
			t.Errorf("agent %d received invalid trunk revision: %d", res.agentID, res.trunkRevision)
		}
		if prevAgent, exists := revisionsSeen[res.trunkRevision]; exists {
			t.Errorf("duplicate trunk revision %d detected! Claimed by both agent %d and agent %d",
				res.trunkRevision, prevAgent, res.agentID)
		}
		revisionsSeen[res.trunkRevision] = res.agentID
	}

	t.Logf("✅ All %d agents submitted successfully. Unique trunk revisions verified: %d", numAgents, len(revisionsSeen))

	// 7. Verify Head CL in Database
	headCL, err := cluster.DB.GetHeadCL(ctx)
	if err != nil {
		t.Fatalf("failed to query GetHeadCL after storm: %v", err)
	}
	if headCL.TrunkRevision == nil || *headCL.TrunkRevision < int64(numAgents) {
		t.Errorf("expected Head CL trunk revision to be >= %d, got %v", numAgents, headCL.TrunkRevision)
	}
}
