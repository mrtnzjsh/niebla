package tree

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
