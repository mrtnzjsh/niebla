package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	nieblav1 "niebla/pkg/proto/nieblav1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCASCLIHelp(t *testing.T) {
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"cas", "--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected cas --help to succeed: %v", err)
	}

	out := buf.String()
	for _, sub := range []string{"put", "get"} {
		if !strings.Contains(out, sub) {
			t.Errorf("expected subcommand %q in cas help output", sub)
		}
	}
}

func TestCASValidationErrors(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Missing arguments
	rootCmd := NewRootCmd()
	rootCmd.SetArgs([]string{"cas", "put"})
	if err := rootCmd.Execute(); err == nil {
		t.Errorf("expected error for 'cas put' with no args")
	}

	// 2. Non-existent file
	rootCmd = NewRootCmd()
	rootCmd.SetArgs([]string{"cas", "put", filepath.Join(tempDir, "nonexistent.bin")})
	if err := rootCmd.Execute(); err == nil {
		t.Errorf("expected error for non-existent file")
	}

	// 3. Directory instead of file
	dirPath := filepath.Join(tempDir, "some-dir")
	_ = os.MkdirAll(dirPath, 0755)
	rootCmd = NewRootCmd()
	rootCmd.SetArgs([]string{"cas", "put", dirPath})
	if err := rootCmd.Execute(); err == nil || !strings.Contains(err.Error(), "cannot put directory") {
		t.Errorf("expected directory error, got: %v", err)
	}

	// 4. Invalid hex in 'cas get'
	rootCmd = NewRootCmd()
	rootCmd.SetArgs([]string{"cas", "get", "not-a-hex-string"})
	if err := rootCmd.Execute(); err == nil || !strings.Contains(err.Error(), "invalid sha256 hex string") {
		t.Errorf("expected invalid hex error, got: %v", err)
	}
}

// mockCASServer implements minimal UploadBlob and ReadBlob for testing the CLI.
type mockCASServer struct {
	nieblav1.UnimplementedNieblaServiceServer
	mu    sync.Mutex
	blobs map[string][]byte
}

func newMockCASServer() *mockCASServer {
	return &mockCASServer{
		blobs: make(map[string][]byte),
	}
}

func (m *mockCASServer) UploadBlob(stream nieblav1.NieblaService_UploadBlobServer) error {
	var data []byte
	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		data = append(data, req.Data...)
	}

	sum := sha256.Sum256(data)
	m.mu.Lock()
	m.blobs[hex.EncodeToString(sum[:])] = data
	m.mu.Unlock()

	return stream.SendAndClose(&nieblav1.UploadBlobResponse{
		Sha256: sum[:],
		Size:   uint64(len(data)),
	})
}

func (m *mockCASServer) ReadBlob(req *nieblav1.ReadBlobRequest, stream nieblav1.NieblaService_ReadBlobServer) error {
	hexKey := hex.EncodeToString(req.Sha256)
	m.mu.Lock()
	data, ok := m.blobs[hexKey]
	m.mu.Unlock()

	if !ok {
		return status.Errorf(codes.NotFound, "blob not found")
	}

	chunkSize := 1024
	for offset := 0; offset < len(data); offset += chunkSize {
		end := offset + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := stream.Send(&nieblav1.BlobChunk{Data: data[offset:end]}); err != nil {
			return err
		}
	}
	return nil
}

func TestCASPutAndGet_EndToEnd(t *testing.T) {
	// Start in-process mock server
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	grpcServer := grpc.NewServer()
	mockSrv := newMockCASServer()
	nieblav1.RegisterNieblaServiceServer(grpcServer, mockSrv)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.Stop()

	serverAddr := lis.Addr().String()
	tempDir := t.TempDir()

	// Create test binary file
	testData := []byte("binary executable content for hook testing 1234567890")
	expectedSum := sha256.Sum256(testData)
	expectedHex := hex.EncodeToString(expectedSum[:])

	binPath := filepath.Join(tempDir, "custom-linter")
	if err := os.WriteFile(binPath, testData, 0755); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// 1. Put file with --json
	putBuf := new(bytes.Buffer)
	rootCmd := NewRootCmd()
	rootCmd.SetOut(putBuf)
	rootCmd.SetArgs([]string{
		"cas", "put", binPath,
		"--server", serverAddr,
		"--json",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("cas put failed: %v", err)
	}

	var putOutput CASPutOutput
	if err := json.Unmarshal(putBuf.Bytes(), &putOutput); err != nil {
		t.Fatalf("failed to parse json output: %v\nOutput: %s", err, putBuf.String())
	}

	if putOutput.SHA256 != expectedHex {
		t.Errorf("expected sha256 %s, got %s", expectedHex, putOutput.SHA256)
	}
	if putOutput.SizeBytes != uint64(len(testData)) {
		t.Errorf("expected size %d, got %d", len(testData), putOutput.SizeBytes)
	}

	// 2. Get file by SHA-256 to destination file
	downloadedPath := filepath.Join(tempDir, "downloaded-linter")
	getBuf := new(bytes.Buffer)
	rootCmd = NewRootCmd()
	rootCmd.SetOut(getBuf)
	rootCmd.SetArgs([]string{
		"cas", "get", expectedHex,
		"--out", downloadedPath,
		"--server", serverAddr,
		"--json",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("cas get failed: %v", err)
	}

	var getOutput CASGetOutput
	if err := json.Unmarshal(getBuf.Bytes(), &getOutput); err != nil {
		t.Fatalf("failed to parse get json output: %v\nOutput: %s", err, getBuf.String())
	}

	if getOutput.SHA256 != expectedHex || getOutput.SizeBytes != uint64(len(testData)) {
		t.Errorf("unexpected getOutput: %+v", getOutput)
	}

	downloadedBytes, err := os.ReadFile(downloadedPath)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if !bytes.Equal(downloadedBytes, testData) {
		t.Fatalf("downloaded content mismatch")
	}
}
