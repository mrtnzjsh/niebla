//go:build rc

package rc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"path/filepath"
	"testing"

	"niebla/pkg/cas"
	nieblav1 "niebla/pkg/proto/nieblav1"
)

func TestMultiRegionCASReplicationAndStreaming(t *testing.T) {
	cluster := SetupRCCluster(t)
	defer cluster.Cleanup()

	ctx := context.Background()

	// 1. Write a 2MB synthetic payload into Region A (authoritative server CAS)
	largePayload := make([]byte, 2*1024*1024)
	for i := range largePayload {
		largePayload[i] = byte((i * 31) % 256)
	}
	expectedHash := sha256.Sum256(largePayload)

	blobInfoA, err := cluster.CASStorage.Put(bytes.NewReader(largePayload))
	if err != nil {
		t.Fatalf("failed to store blob in Region A CAS: %v", err)
	}
	if !bytes.Equal(blobInfoA.HashBytes, expectedHash[:]) {
		t.Fatalf("Region A stored hash mismatch")
	}

	// 2. Set up Region B (simulating a remote workspace or edge node with its own local CAS cache)
	regionBDir := filepath.Join(t.TempDir(), "region-b-cas")
	storageB, err := cas.NewStorage(regionBDir)
	if err != nil {
		t.Fatalf("failed to init Region B CAS: %v", err)
	}

	// Verify Region B initially DOES NOT have this blob
	if storageB.Exists(expectedHash[:]) {
		t.Fatalf("Region B should not contain the blob before streaming sync")
	}

	// 3. Region B streams the blob on-demand from the server (Region A) via gRPC
	streamClient, err := cluster.GRPCClient.GetBlob(ctx, &nieblav1.GetBlobRequest{
		Sha256: expectedHash[:],
	})
	if err != nil {
		t.Fatalf("DownloadBlob gRPC call failed: %v", err)
	}

	writerB, err := storageB.BeginWriter()
	if err != nil {
		t.Fatalf("failed to begin blob writer in Region B: %v", err)
	}
	defer writerB.Abort()

	for {
		chunk, err := streamClient.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to receive blob chunk from server: %v", err)
		}
		if _, err := writerB.Write(chunk.Data); err != nil {
			t.Fatalf("failed to write chunk to Region B CAS: %v", err)
		}
	}

	blobInfoB, err := writerB.Commit()
	if err != nil {
		t.Fatalf("failed to commit blob in Region B: %v", err)
	}

	// 4. Verify bit-for-bit integrity in Region B
	if !bytes.Equal(blobInfoB.HashBytes, expectedHash[:]) {
		t.Fatalf("Region B committed hash mismatch: got %x, want %x", blobInfoB.HashBytes, expectedHash)
	}
	if blobInfoB.Size != int64(len(largePayload)) {
		t.Fatalf("Region B committed size mismatch: got %d, want %d", blobInfoB.Size, len(largePayload))
	}
	if !storageB.Exists(expectedHash[:]) {
		t.Fatalf("storageB.Exists returned false after successful commit")
	}

	// 5. Verify local read-back from Region B
	f, size, err := storageB.OpenBlob(expectedHash[:])
	if err != nil {
		t.Fatalf("failed to open blob from Region B: %v", err)
	}
	defer f.Close()

	if size != int64(len(largePayload)) {
		t.Errorf("read-back size mismatch: got %d, want %d", size, len(largePayload))
	}

	readBackContent, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("failed to read back content: %v", err)
	}
	if !bytes.Equal(readBackContent, largePayload) {
		t.Errorf("read back bytes in Region B do not match original payload")
	}

	t.Logf("✅ Successfully verified multi-region on-demand CAS streaming: %d bytes replicated with SHA-256 %s",
		size, blobInfoB.HexHash)
}
