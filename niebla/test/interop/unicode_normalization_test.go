package interop_test

import (
	"bytes"
	"testing"

	"golang.org/x/text/unicode/norm"
	"niebla/pkg/tree"
)

func TestUnicodeNormalization_APFS_NFD_vs_Linux_NFC(t *testing.T) {
	// Sample words with accents:
	// "café": NFC is [c, a, f, \u00e9], NFD is [c, a, f, e, \u0301]
	// "schön": NFC is [s, c, h, \u00f6, n], NFD is [s, c, h, o, \u0308, n]
	// "naïve": NFC is [n, a, \u00ef, v, e], NFD is [n, a, i, \u0308, v, e]

	testCases := []struct {
		original string
	}{
		{"café.go"},
		{"schön_test.go"},
		{"naïve_bayes.py"},
	}

	for _, tc := range testCases {
		nfcName := norm.NFC.String(tc.original)
		nfdName := norm.NFD.String(tc.original)

		// Sanity check that NFC and NFD byte representations actually differ
		if nfcName == nfdName {
			t.Fatalf("expected NFC and NFD byte sequences to differ for %q", tc.original)
		}

		dummyHash := []byte("01234567890123456789012345678901")

		// Merkle entry compiled on Linux / NFC
		entriesNFC := []tree.Entry{
			{Name: nfcName, Mode: 0100644, Size: 100, BlobSHA256: dummyHash},
			{Name: "main.go", Mode: 0100644, Size: 200, BlobSHA256: dummyHash},
		}
		hashNFC := tree.ComputeTreeHash(entriesNFC)

		// Merkle entry compiled on macOS APFS / NFD
		entriesNFD := []tree.Entry{
			{Name: nfdName, Mode: 0100644, Size: 100, BlobSHA256: dummyHash},
			{Name: "main.go", Mode: 0100644, Size: 200, BlobSHA256: dummyHash},
		}
		hashNFD := tree.ComputeTreeHash(entriesNFD)

		// Invariant: Both must yield bit-for-bit identical cryptographic tree hashes
		if !bytes.Equal(hashNFC, hashNFD) {
			t.Errorf("Merkle tree hash mismatch for %q: NFC=%x, NFD=%x", tc.original, hashNFC, hashNFD)
		}
	}
}
