package cas

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestStoragePartitioningAndWrite(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cas-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	testData := []byte("hello world, Niebla CAS blob storage engine!")
	expectedHash := sha256.Sum256(testData)
	expectedHex := hex.EncodeToString(expectedHash[:])

	// 1. Test Put
	hashBytes, hexHash, size, storagePath, err := storage.Put(bytes.NewReader(testData))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	if hexHash != expectedHex {
		t.Errorf("expected hash %s, got %s", expectedHex, hexHash)
	}
	if !bytes.Equal(hashBytes, expectedHash[:]) {
		t.Errorf("hash bytes mismatch")
	}
	if size != int64(len(testData)) {
		t.Errorf("expected size %d, got %d", len(testData), size)
	}

	expectedRelPath := filepath.ToSlash(filepath.Join(expectedHex[0:2], expectedHex[2:4], expectedHex))
	if storagePath != expectedRelPath {
		t.Errorf("expected portable relative key %s, got %s", expectedRelPath, storagePath)
	}

	expectedAbsPath := filepath.Join(tmpDir, filepath.FromSlash(expectedRelPath))
	if resolved := storage.ResolveStoragePath(storagePath); resolved != expectedAbsPath {
		t.Errorf("expected resolved path %s, got %s", expectedAbsPath, resolved)
	}

	if !storage.Exists(expectedHash[:]) {
		t.Fatalf("expected blob to exist")
	}

	// 2. Test StreamBlob
	var streamedData []byte
	err = storage.StreamBlob(expectedHash[:], 0, 0, 8, func(chunk []byte) error {
		streamedData = append(streamedData, chunk...)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamBlob failed: %v", err)
	}
	if !bytes.Equal(streamedData, testData) {
		t.Errorf("streamed data mismatch: got %q, want %q", string(streamedData), string(testData))
	}

	// 3. Test StreamBlob with offset and length
	var sliceData []byte
	offset := uint64(6)
	length := uint64(5) // "world"
	err = storage.StreamBlob(expectedHash[:], offset, length, 2, func(chunk []byte) error {
		sliceData = append(sliceData, chunk...)
		return nil
	})
	if err != nil {
		t.Fatalf("StreamBlob with offset/length failed: %v", err)
	}
	if string(sliceData) != "world" {
		t.Errorf("expected 'world', got %q", string(sliceData))
	}
}

func TestBlobWriterAbort(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "cas-abort-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	storage, err := NewStorage(tmpDir)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	writer, err := storage.BeginWriter()
	if err != nil {
		t.Fatalf("BeginWriter failed: %v", err)
	}

	_, _ = writer.Write([]byte("some aborted payload"))
	writer.Abort()

	// Ensure no orphaned files in tmp
	entries, err := os.ReadDir(filepath.Join(tmpDir, tmpSubDir))
	if err != nil {
		t.Fatalf("ReadDir on tmpDir failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 files in tmp directory, got %d", len(entries))
	}
}

func TestStorageRelocationWithRelativeKeys(t *testing.T) {
	dirA, err := os.MkdirTemp("", "cas-reloc-a-*")
	if err != nil {
		t.Fatalf("failed to create temp dir A: %v", err)
	}
	defer os.RemoveAll(dirA)

	storageA, err := NewStorage(dirA)
	if err != nil {
		t.Fatalf("NewStorage(dirA) failed: %v", err)
	}

	payload := []byte("portable relative CAS storage key verification")
	hashBytes, _, _, relKey, err := storageA.Put(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}
	if filepath.IsAbs(relKey) {
		t.Fatalf("expected relative storage key, got absolute path %q", relKey)
	}

	// Simulate relocating the entire CAS directory to a new mount/host path
	dirB := dirA + "-moved"
	if err := os.Rename(dirA, dirB); err != nil {
		t.Fatalf("failed to relocate CAS directory: %v", err)
	}
	defer os.RemoveAll(dirB)

	storageB, err := NewStorage(dirB)
	if err != nil {
		t.Fatalf("NewStorage(dirB) failed: %v", err)
	}

	if !storageB.Exists(hashBytes) {
		t.Fatalf("expected blob to exist after relocating baseDir")
	}

	var out []byte
	if err := storageB.StreamBlob(hashBytes, 0, 0, 16, func(chunk []byte) error {
		out = append(out, chunk...)
		return nil
	}); err != nil {
		t.Fatalf("StreamBlob on relocated storage failed: %v", err)
	}
	if !bytes.Equal(out, payload) {
		t.Errorf("relocated blob content mismatch: got %q, want %q", string(out), string(payload))
	}
}
