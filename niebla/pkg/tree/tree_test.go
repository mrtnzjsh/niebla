package tree

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

func TestEmptyTreeHash(t *testing.T) {
	emptyHash := sha256.Sum256([]byte{})
	if !bytes.Equal(EmptyTreeHash, emptyHash[:]) {
		t.Fatalf("EmptyTreeHash does not match sha256 of empty bytes")
	}

	computed := ComputeTreeHash(nil)
	if !bytes.Equal(computed, EmptyTreeHash) {
		t.Errorf("ComputeTreeHash(nil) = %s, want %s", hex.EncodeToString(computed), hex.EncodeToString(EmptyTreeHash))
	}

	computedEmpty := ComputeTreeHash([]Entry{})
	if !bytes.Equal(computedEmpty, EmptyTreeHash) {
		t.Errorf("ComputeTreeHash([]) = %s, want %s", hex.EncodeToString(computedEmpty), hex.EncodeToString(EmptyTreeHash))
	}
}

func TestTreeHashDeterministic(t *testing.T) {
	blob1 := sha256.Sum256([]byte("content 1"))
	blob2 := sha256.Sum256([]byte("content 2"))

	entriesA := []Entry{
		{Name: "zebra.txt", Mode: 0100644, Size: 9, BlobSHA256: blob1[:]},
		{Name: "apple.txt", Mode: 0100644, Size: 9, BlobSHA256: blob2[:]},
	}

	entriesB := []Entry{
		{Name: "apple.txt", Mode: 0100644, Size: 9, BlobSHA256: blob2[:]},
		{Name: "zebra.txt", Mode: 0100644, Size: 9, BlobSHA256: blob1[:]},
	}

	hashA := ComputeTreeHash(entriesA)
	hashB := ComputeTreeHash(entriesB)

	if !bytes.Equal(hashA, hashB) {
		t.Errorf("hashes should be deterministic regardless of input order")
	}

	// Verify FindEntry
	SortEntries(entriesA)
	found, ok := FindEntry(entriesA, "apple.txt")
	if !ok || found.Name != "apple.txt" {
		t.Errorf("expected to find apple.txt")
	}

	_, ok = FindEntry(entriesA, "banana.txt")
	if ok {
		t.Errorf("expected banana.txt not to be found")
	}
}

func TestResolvePath(t *testing.T) {
	fileBlob := sha256.Sum256([]byte("main.go code"))

	srcEntries := []Entry{
		{Name: "main.go", Mode: 0100644, Size: 12, BlobSHA256: fileBlob[:]},
	}
	srcTreeHash := ComputeTreeHash(srcEntries)

	rootEntries := []Entry{
		{Name: "src", Mode: 0040755, SubTreeHash: srcTreeHash},
		{Name: "README.md", Mode: 0100644, Size: 20, BlobSHA256: fileBlob[:]},
	}
	rootTreeHash := ComputeTreeHash(rootEntries)

	fetcher := func(hash []byte) ([]Entry, error) {
		if bytes.Equal(hash, rootTreeHash) {
			return rootEntries, nil
		}
		if bytes.Equal(hash, srcTreeHash) {
			return srcEntries, nil
		}
		return nil, nil
	}

	// Test root resolution
	r, err := ResolvePath(rootTreeHash, "", fetcher)
	if err != nil || !r.IsDir() {
		t.Fatalf("failed to resolve root: %v", err)
	}

	// Test README resolution
	readme, err := ResolvePath(rootTreeHash, "README.md", fetcher)
	if err != nil || readme.Name != "README.md" {
		t.Fatalf("failed to resolve README.md: %v", err)
	}

	// Test nested path resolution
	nested, err := ResolvePath(rootTreeHash, "src/main.go", fetcher)
	if err != nil || nested.Name != "main.go" {
		t.Fatalf("failed to resolve src/main.go: %v", err)
	}

	// Test proto conversions
	protoNode := nested.ToProto()
	roundTrip := FromProto(protoNode)
	if roundTrip.Name != nested.Name || roundTrip.Size != nested.Size {
		t.Errorf("roundtrip proto conversion failed")
	}
}

func TestMarshalUnmarshalManifest(t *testing.T) {
	// 1. Empty manifest
	emptyData, err := MarshalManifest(nil)
	if err != nil {
		t.Fatalf("MarshalManifest(nil) failed: %v", err)
	}
	if len(emptyData) != 0 {
		t.Errorf("expected empty slice for empty manifest, got %d bytes", len(emptyData))
	}
	emptyEntries, err := UnmarshalManifest(emptyData)
	if err != nil || len(emptyEntries) != 0 {
		t.Fatalf("UnmarshalManifest(empty) failed: %v, len=%d", err, len(emptyEntries))
	}

	// 2. Single and mixed file/dir entries
	blobHash := sha256.Sum256([]byte("file content"))
	subHash := sha256.Sum256([]byte("subtree content"))
	mixed := []Entry{
		{Name: "z_dir", Mode: 0040755, Size: 0, SubTreeHash: subHash[:]},
		{Name: "a_file.go", Mode: 0100644, Size: 1234, BlobSHA256: blobHash[:]},
	}

	manifestBytes, err := MarshalManifest(mixed)
	if err != nil {
		t.Fatalf("MarshalManifest(mixed) failed: %v", err)
	}

	// Verify SHA256(manifestBytes) matches ComputeTreeHash(mixed)
	manifestHash := sha256.Sum256(manifestBytes)
	if !bytes.Equal(manifestHash[:], ComputeTreeHash(mixed)) {
		t.Errorf("manifest SHA-256 does not match ComputeTreeHash")
	}

	decoded, err := UnmarshalManifest(manifestBytes)
	if err != nil {
		t.Fatalf("UnmarshalManifest(mixed) failed: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(decoded))
	}
	// Should be sorted lexicographically
	if decoded[0].Name != "a_file.go" || decoded[0].Size != 1234 || !bytes.Equal(decoded[0].BlobSHA256, blobHash[:]) || decoded[0].SubTreeHash != nil {
		t.Errorf("unexpected decoded[0]: %+v", decoded[0])
	}
	if decoded[1].Name != "z_dir" || !decoded[1].IsDir() || !bytes.Equal(decoded[1].SubTreeHash, subHash[:]) || decoded[1].BlobSHA256 != nil {
		t.Errorf("unexpected decoded[1]: %+v", decoded[1])
	}

	// 3. Large 1,000-entry directory tree round-trip
	large := make([]Entry, 1000)
	for i := 0; i < 1000; i++ {
		h := sha256.Sum256([]byte(fmt.Sprintf("blob-%d", i)))
		large[i] = Entry{
			Name:       fmt.Sprintf("file_%04d.txt", 999-i), // intentionally reverse-sorted
			Mode:       0100644,
			Size:       uint64(i * 10),
			BlobSHA256: h[:],
		}
	}

	largeManifest, err := MarshalManifest(large)
	if err != nil {
		t.Fatalf("MarshalManifest(1000) failed: %v", err)
	}
	largeDecoded, err := UnmarshalManifest(largeManifest)
	if err != nil {
		t.Fatalf("UnmarshalManifest(1000) failed: %v", err)
	}
	if len(largeDecoded) != 1000 {
		t.Fatalf("expected 1000 decoded entries, got %d", len(largeDecoded))
	}
	if largeDecoded[0].Name != "file_0000.txt" || largeDecoded[999].Name != "file_0999.txt" {
		t.Errorf("expected sorted 1000 entries, got first=%s last=%s", largeDecoded[0].Name, largeDecoded[999].Name)
	}
	if !bytes.Equal(ComputeTreeHash(large), ComputeTreeHash(largeDecoded)) {
		t.Errorf("tree hash mismatch after 1000-entry round-trip")
	}

	// 4. Truncated / corrupt manifest error handling
	if _, err := UnmarshalManifest(manifestBytes[:5]); err == nil {
		t.Errorf("expected error for truncated manifest")
	}
}
