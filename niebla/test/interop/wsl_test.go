package interop_test

import (
	"bytes"
	"crypto/sha256"
	"strings"
	"testing"

	"niebla/pkg/tree"
)

// TestWSLPathTranslation verifies bidirectional conversion between WSL POSIX paths
// (/mnt/c/...) and Windows DOS/UNC paths (C:\...).
func TestWSLPathTranslation(t *testing.T) {
	testCases := []struct {
		wslPath string
		winPath string
	}{
		{"/mnt/c/Developer/niebla", `C:\Developer\niebla`},
		{"/mnt/c/Users/Developer/workspace", `C:\Users\Developer\workspace`},
		{"/mnt/d/Data/cas/blobs", `D:\Data\cas\blobs`},
		{"/mnt/z/shared", `Z:\shared`},
	}

	wslToWin := func(p string) string {
		p = strings.TrimPrefix(p, "/")
		if strings.HasPrefix(p, "mnt/") && len(p) >= 5 {
			drive := strings.ToUpper(string(p[4]))
			rest := p[5:]
			rest = strings.ReplaceAll(rest, "/", "\\")
			return drive + ":" + rest
		}
		return p
	}

	winToWSL := func(p string) string {
		p = strings.ReplaceAll(p, "\\", "/")
		if len(p) >= 2 && p[1] == ':' {
			drive := strings.ToLower(string(p[0]))
			rest := p[2:]
			return "/mnt/" + drive + rest
		}
		return p
	}

	for _, tc := range testCases {
		gotWin := wslToWin(tc.wslPath)
		if gotWin != tc.winPath {
			t.Errorf("wslToWin(%q) = %q; expected %q", tc.wslPath, gotWin, tc.winPath)
		}

		gotWSL := winToWSL(tc.winPath)
		if gotWSL != tc.wslPath {
			t.Errorf("winToWSL(%q) = %q; expected %q", tc.winPath, gotWSL, tc.wslPath)
		}
	}
}

// TestWSLLineEndingNormalization verifies that when files are accessed across Windows (CRLF)
// and WSL Linux (LF), Merkle tree hashes and blob tracking can distinguish or normalize cleanly.
func TestWSLLineEndingNormalization(t *testing.T) {
	contentLF := []byte("package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")
	contentCRLF := []byte("package main\r\n\r\nfunc main() {\r\n\tprintln(\"hello\")\r\n}\r\n")

	hashLF := sha256.Sum256(contentLF)
	hashCRLF := sha256.Sum256(contentCRLF)

	// Raw byte hashes must differ
	if bytes.Equal(hashLF[:], hashCRLF[:]) {
		t.Fatalf("expected raw CRLF and LF byte hashes to differ")
	}

	// Normalizer converts CRLF -> LF for canonical storage
	normalizeLF := func(b []byte) []byte {
		return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	}

	normalizedFromCRLF := normalizeLF(contentCRLF)
	if !bytes.Equal(normalizedFromCRLF, contentLF) {
		t.Fatalf("normalizing CRLF did not yield expected LF content")
	}

	normHash := sha256.Sum256(normalizedFromCRLF)
	if !bytes.Equal(normHash[:], hashLF[:]) {
		t.Fatalf("normalized hash did not match LF hash")
	}
}

// TestWSLDrvFSPermissionMasking tests that DrvFS default synthetic 0777 modes on Windows mounts
// do not cause phantom permission mutations in Merkle tree entries.
func TestWSLDrvFSPermissionMasking(t *testing.T) {
	// Standard canonical Git/Niebla modes:
	// 0100644 = Regular non-executable file
	// 0100755 = Executable binary/script
	// DrvFS often mounts NTFS files as 0777 or 0755 by default if metadata flag is absent.

	canonicalizeMode := func(mode uint32, isScriptOrBinary bool) uint32 {
		// Strip umask/drvfs permission bits and map to canonical 0100644 or 0100755
		if mode&0111 != 0 && isScriptOrBinary {
			return 0100755
		}
		return 0100644
	}

	testCases := []struct {
		rawDrvFSMode     uint32
		isScriptOrBinary bool
		expectedMode     uint32
	}{
		{0777, false, 0100644}, // Text/Go file mounted via DrvFS with synthetic 777
		{0777, true, 0100755},  // Executable script/binary
		{0644, false, 0100644}, // Native Linux mode
		{0755, true, 0100755},  // Native Linux executable mode
	}

	for _, tc := range testCases {
		canon := canonicalizeMode(tc.rawDrvFSMode, tc.isScriptOrBinary)
		if canon != tc.expectedMode {
			t.Errorf("canonicalizeMode(%04o, %v) = %04o; expected %04o",
				tc.rawDrvFSMode, tc.isScriptOrBinary, canon, tc.expectedMode)
		}
	}

	// Invariant: Two tree entries with canonical modes produce identical tree hash
	dummyHash := []byte("01234567890123456789012345678901")
	entryDrvFS := []tree.Entry{
		{Name: "app.go", Mode: canonicalizeMode(0777, false), Size: 100, BlobSHA256: dummyHash},
	}
	entryLinux := []tree.Entry{
		{Name: "app.go", Mode: canonicalizeMode(0644, false), Size: 100, BlobSHA256: dummyHash},
	}

	hashDrvFS := tree.ComputeTreeHash(entryDrvFS)
	hashLinux := tree.ComputeTreeHash(entryLinux)

	if !bytes.Equal(hashDrvFS, hashLinux) {
		t.Fatalf("tree hash mismatch between DrvFS masked mode and Linux mode")
	}
}
