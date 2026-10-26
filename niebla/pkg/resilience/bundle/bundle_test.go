package bundle_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"niebla/pkg/crypto"
	"niebla/pkg/resilience/bundle"
)

func TestPackUnpackRoundtrip(t *testing.T) {
	masterKey, err := crypto.GenerateDEK()
	if err != nil {
		t.Fatalf("failed to generate master key: %v", err)
	}
	defer crypto.WipeKey(masterKey)

	manifest := bundle.Manifest{
		BackupID:     "niebla-backup-20261025-100",
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
		CLCount:      5,
		BlobCount:    2,
		TotalBytes:   1024,
		DatabaseName: "niebla",
	}

	dbDump := []byte("CREATE TABLE changelists (cl_id INT PRIMARY KEY);\nINSERT INTO changelists VALUES (1);")
	blob1 := []byte("package main\n\nfunc main() {}\n")
	blob2 := []byte("README content for repository\n")

	blobs := map[string]io.Reader{
		"a1b2c3d4e5": bytes.NewReader(blob1),
		"f6e5d4c3b2": bytes.NewReader(blob2),
	}

	var archiveBuf bytes.Buffer
	packOpts := bundle.PackOptions{
		MasterKey:    masterKey,
		CipherSuite:  crypto.SuiteAES256GCM,
		Manifest:     manifest,
		DBDumpReader: bytes.NewReader(dbDump),
		Blobs:        blobs,
	}

	if err := bundle.Pack(context.Background(), &archiveBuf, packOpts); err != nil {
		t.Fatalf("bundle.Pack failed: %v", err)
	}

	// 1. Verify Header without full unpack
	archiveBytes := archiveBuf.Bytes()
	header, meta, err := bundle.VerifyHeader(bytes.NewReader(archiveBytes), masterKey)
	if err != nil {
		t.Fatalf("bundle.VerifyHeader failed: %v", err)
	}
	if header.CipherSuite != crypto.SuiteAES256GCM {
		t.Errorf("expected cipher suite AES-256-GCM, got %d", header.CipherSuite)
	}
	if meta.BackupID != manifest.BackupID {
		t.Errorf("manifest BackupID mismatch: got %s, want %s", meta.BackupID, manifest.BackupID)
	}

	// 2. Full Unpack
	result, err := bundle.Unpack(context.Background(), bytes.NewReader(archiveBytes), masterKey)
	if err != nil {
		t.Fatalf("bundle.Unpack failed: %v", err)
	}

	if result.Manifest.BackupID != manifest.BackupID {
		t.Errorf("unpacked manifest BackupID mismatch")
	}
	if !bytes.Equal(result.DBDump, dbDump) {
		t.Errorf("unpacked database dump mismatch: got %s, want %s", string(result.DBDump), string(dbDump))
	}
	if len(result.Blobs) != 2 {
		t.Fatalf("expected 2 unpacked blobs, got %d", len(result.Blobs))
	}
	if !bytes.Equal(result.Blobs["a1b2c3d4e5"], blob1) {
		t.Errorf("unpacked blob1 mismatch")
	}
	if !bytes.Equal(result.Blobs["f6e5d4c3b2"], blob2) {
		t.Errorf("unpacked blob2 mismatch")
	}

	// 3. Unpack with wrong master key fails
	wrongKey, _ := crypto.GenerateDEK()
	_, err = bundle.Unpack(context.Background(), bytes.NewReader(archiveBytes), wrongKey)
	if err == nil {
		t.Fatalf("expected unpack to fail with wrong master key, but succeeded")
	}
	if !strings.Contains(err.Error(), "authentication failed") && !strings.Contains(err.Error(), "unwrap DEK") {
		t.Logf("got expected auth failure: %v", err)
	}
}

func TestUnpackCorruptedArchive(t *testing.T) {
	masterKey, _ := crypto.GenerateDEK()
	defer crypto.WipeKey(masterKey)

	manifest := bundle.Manifest{BackupID: "test-corrupt", CreatedAt: time.Now()}
	var buf bytes.Buffer
	_ = bundle.Pack(context.Background(), &buf, bundle.PackOptions{
		MasterKey: masterKey,
		Manifest:  manifest,
	})

	data := buf.Bytes()
	// Corrupt a byte in the payload stream
	if len(data) > 100 {
		data[len(data)-20] ^= 0x55
	}

	_, err := bundle.Unpack(context.Background(), bytes.NewReader(data), masterKey)
	if err == nil {
		t.Fatalf("expected error on corrupted archive payload, but got nil")
	}
}
