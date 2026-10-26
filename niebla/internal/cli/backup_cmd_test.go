package cli

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupCLIHelp(t *testing.T) {
	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"backup", "--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected backup --help to succeed: %v", err)
	}

	out := buf.String()
	for _, sub := range []string{"create", "list", "verify", "restore", "prune"} {
		if !strings.Contains(out, sub) {
			t.Errorf("expected subcommand %q in backup help output", sub)
		}
	}
}

func TestBackupBundleWorkflow(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "niebla-backup-cli-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	backupDir := filepath.Join(tempDir, "backups")
	restoreDir := filepath.Join(tempDir, "restored")
	_ = os.MkdirAll(backupDir, 0755)

	// Set 32-byte test master key
	testKeyHex := hex.EncodeToString(bytes.Repeat([]byte{0x42}, 32))
	t.Setenv("NIEBLA_MASTER_KEY", testKeyHex)
	t.Setenv("NIEBLA_CONFIG", filepath.Join(tempDir, "config.toml"))

	rootCmd := NewRootCmd()

	// 1. Create a bundle backup targeting the test backupDir
	createBuf := new(bytes.Buffer)
	rootCmd.SetOut(createBuf)
	rootCmd.SetErr(createBuf)
	rootCmd.SetArgs([]string{
		"backup", "create",
		"--mode", "bundle",
		"--target", "filesystem",
		"--dir", backupDir,
		"--label", "unit-test",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("backup create failed: %v\nOutput: %s", err, createBuf.String())
	}

	createOut := createBuf.String()
	if !strings.Contains(createOut, "Encrypted bundle successfully stored") {
		t.Fatalf("unexpected create output: %s", createOut)
	}

	// 2. List backups
	listBuf := new(bytes.Buffer)
	rootCmd.SetOut(listBuf)
	rootCmd.SetErr(listBuf)
	rootCmd.SetArgs([]string{
		"backup", "list",
		"--target", "filesystem",
		"--dir", backupDir,
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("backup list failed: %v", err)
	}
	listOut := listBuf.String()
	if !strings.Contains(listOut, "niebla-unit-test-") || !strings.Contains(listOut, ".nieblabak") {
		t.Fatalf("expected created backup in list output: %s", listOut)
	}

	// Find the created backup file name
	var createdFileName string
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("failed to read test backup dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "niebla-unit-test-") && strings.HasSuffix(e.Name(), ".nieblabak") {
			createdFileName = e.Name()
			break
		}
	}
	if createdFileName == "" {
		t.Fatalf("could not find created backup file on disk in %s", backupDir)
	}

	// 3. Verify backup
	verifyBuf := new(bytes.Buffer)
	rootCmd.SetOut(verifyBuf)
	rootCmd.SetErr(verifyBuf)
	rootCmd.SetArgs([]string{
		"backup", "verify", createdFileName,
		"--target", "filesystem",
		"--dir", backupDir,
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("backup verify failed: %v\nOutput: %s", err, verifyBuf.String())
	}
	verifyOut := verifyBuf.String()
	if !strings.Contains(verifyOut, "Backup Archive Verification Succeeded") || !strings.Contains(verifyOut, "AES-256-GCM") {
		t.Fatalf("unexpected verify output: %s", verifyOut)
	}

	// 4. Restore with --dry-run
	dryRunBuf := new(bytes.Buffer)
	rootCmd.SetOut(dryRunBuf)
	rootCmd.SetErr(dryRunBuf)
	rootCmd.SetArgs([]string{
		"backup", "restore", createdFileName,
		"--target", "filesystem",
		"--dir", backupDir,
		"--dry-run",
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("backup restore --dry-run failed: %v\nOutput: %s", err, dryRunBuf.String())
	}
	dryRunOut := dryRunBuf.String()
	if !strings.Contains(dryRunOut, "[DRY-RUN] ✓ Cryptographic integrity verified!") {
		t.Fatalf("unexpected dry-run output: %s", dryRunOut)
	}

	// 5. Actual Restore
	rootCmd = NewRootCmd()
	restoreBuf := new(bytes.Buffer)
	rootCmd.SetOut(restoreBuf)
	rootCmd.SetErr(restoreBuf)
	rootCmd.SetArgs([]string{
		"backup", "restore", createdFileName,
		"--target", "filesystem",
		"--dir", backupDir,
		"--dest", restoreDir,
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("backup restore failed: %v\nOutput: %s", err, restoreBuf.String())
	}
	restoreOut := restoreBuf.String()
	if !strings.Contains(restoreOut, "Successfully restored backup") {
		t.Fatalf("unexpected restore output: %s", restoreOut)
	}

	// Verify extracted blob
	metaPath := filepath.Join(restoreDir, "blobs", "meta.json")
	if metaBytes, err := os.ReadFile(metaPath); err != nil {
		t.Fatalf("failed to read extracted meta.json: %v", err)
	} else if !strings.Contains(string(metaBytes), "unit-test") {
		t.Fatalf("unexpected content in restored meta.json: %s", string(metaBytes))
	}

	// 6. Prune
	rootCmd = NewRootCmd()
	pruneBuf := new(bytes.Buffer)
	rootCmd.SetOut(pruneBuf)
	rootCmd.SetErr(pruneBuf)
	rootCmd.SetArgs([]string{
		"backup", "prune",
		"--keep", "5",
		"--target", "filesystem",
		"--dir", backupDir,
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("backup prune failed: %v\nOutput: %s", err, pruneBuf.String())
	}
	pruneOut := pruneBuf.String()
	if !strings.Contains(pruneOut, "Nothing to prune") {
		t.Fatalf("unexpected prune output: %s", pruneOut)
	}
}
