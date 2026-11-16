package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"niebla/pkg/tree"
)

func TestScanScratchChanges(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "niebla-status-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create base tree with:
	// - "existing.txt"
	// - "to_delete.txt"
	// - "nested/orig.txt"
	baseExistingSHA := sha256.Sum256([]byte("existing original"))
	baseToDeleteSHA := sha256.Sum256([]byte("delete me"))
	baseNestedSHA := sha256.Sum256([]byte("nested orig"))

	nestedEntries := []tree.Entry{
		{
			Name:       "orig.txt",
			Mode:       0100644,
			Size:       uint64(len("nested orig")),
			BlobSHA256: baseNestedSHA[:],
		},
	}
	nestedTreeHash := tree.ComputeTreeHash(nestedEntries)

	baseEntries := []tree.Entry{
		{
			Name:       "existing.txt",
			Mode:       0100644,
			Size:       uint64(len("existing original")),
			BlobSHA256: baseExistingSHA[:],
		},
		{
			Name:        "nested",
			Mode:        0040755,
			SubTreeHash: nestedTreeHash,
		},
		{
			Name:       "to_delete.txt",
			Mode:       0100644,
			Size:       uint64(len("delete me")),
			BlobSHA256: baseToDeleteSHA[:],
		},
	}
	rootTreeHash := tree.ComputeTreeHash(baseEntries)

	treeMap := map[string][]tree.Entry{
		string(rootTreeHash):   baseEntries,
		string(nestedTreeHash): nestedEntries,
	}
	mockFetch := func(th []byte) ([]tree.Entry, error) {
		if e, ok := treeMap[string(th)]; ok {
			return e, nil
		}
		return []tree.Entry{}, nil
	}

	scratchDir := filepath.Join(tempDir, "scratch")
	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		t.Fatalf("failed to create scratch dir: %v", err)
	}

	// 1. Modified file: existing.txt
	if err := os.WriteFile(filepath.Join(scratchDir, "existing.txt"), []byte("modified content"), 0644); err != nil {
		t.Fatalf("failed to write existing.txt: %v", err)
	}

	// 2. Added file: added.txt
	if err := os.WriteFile(filepath.Join(scratchDir, "added.txt"), []byte("new file content"), 0644); err != nil {
		t.Fatalf("failed to write added.txt: %v", err)
	}

	// 3. Deleted file via prefix whiteout: .wh.to_delete.txt
	if err := os.WriteFile(filepath.Join(scratchDir, ".wh.to_delete.txt"), []byte(""), 0644); err != nil {
		t.Fatalf("failed to write whiteout: %v", err)
	}

	// 4. Added file in nested dir: nested/new_nested.txt
	if err := os.MkdirAll(filepath.Join(scratchDir, "nested"), 0755); err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scratchDir, "nested", "new_nested.txt"), []byte("nested new"), 0644); err != nil {
		t.Fatalf("failed to write nested/new_nested.txt: %v", err)
	}

	// 5. Deleted file via .tombstone suffix: nested/orig.txt.tombstone
	if err := os.WriteFile(filepath.Join(scratchDir, "nested", "orig.txt.tombstone"), []byte(""), 0644); err != nil {
		t.Fatalf("failed to write tombstone: %v", err)
	}

	changes, err := ScanScratchChanges(scratchDir, rootTreeHash, mockFetch)
	if err != nil {
		t.Fatalf("ScanScratchChanges failed: %v", err)
	}

	changeTypes := make(map[string]ChangeType)
	for _, c := range changes {
		changeTypes[c.Path] = c.Type
	}

	if changeTypes["existing.txt"] != ChangeModified {
		t.Errorf("expected existing.txt to be Modified, got %v", changeTypes["existing.txt"])
	}
	if changeTypes["added.txt"] != ChangeAdded {
		t.Errorf("expected added.txt to be Added, got %v", changeTypes["added.txt"])
	}
	if changeTypes["to_delete.txt"] != ChangeDeleted {
		t.Errorf("expected to_delete.txt to be Deleted, got %v", changeTypes["to_delete.txt"])
	}
	if changeTypes["nested/new_nested.txt"] != ChangeAdded {
		t.Errorf("expected nested/new_nested.txt to be Added, got %v", changeTypes["nested/new_nested.txt"])
	}
	if changeTypes["nested/orig.txt"] != ChangeDeleted {
		t.Errorf("expected nested/orig.txt to be Deleted, got %v", changeTypes["nested/orig.txt"])
	}

	// Test renderStatus output
	var buf bytes.Buffer
	renderStatus(&buf, changes)
	output := buf.String()

	if !strings.Contains(output, "existing.txt") ||
		!strings.Contains(output, "added.txt") ||
		!strings.Contains(output, "to_delete.txt") {
		t.Errorf("renderStatus output missing expected entries: %s", output)
	}
}

func TestScanScratchChanges_NieblaIgnoreFiltering(t *testing.T) {
	scratchDir := t.TempDir()

	// Write .nieblaignore
	ignoreRules := `
# Ignore build and dependency directories
node_modules/
build/
.niebla/
*.o
*.log
!important.log
`
	if err := os.WriteFile(filepath.Join(scratchDir, ".nieblaignore"), []byte(ignoreRules), 0644); err != nil {
		t.Fatalf("write .nieblaignore: %v", err)
	}

	// Create files in ignored directories
	mustWriteFile(t, filepath.Join(scratchDir, "node_modules", "pkg", "index.js"), "module.exports = {}")
	mustWriteFile(t, filepath.Join(scratchDir, "build", "output.bin"), "binary")
	mustWriteFile(t, filepath.Join(scratchDir, ".niebla", "metadata.json"), "{}")

	// Create ignored files and negated files
	mustWriteFile(t, filepath.Join(scratchDir, "src", "main.o"), "obj")
	mustWriteFile(t, filepath.Join(scratchDir, "debug.log"), "debug")
	mustWriteFile(t, filepath.Join(scratchDir, "important.log"), "keep me")

	// Create normal modified, added, and deleted (whiteout) files
	mustWriteFile(t, filepath.Join(scratchDir, "src", "main.go"), "package main\nfunc main() {}\n")
	mustWriteFile(t, filepath.Join(scratchDir, "README.md"), "# Updated\n")
	mustWriteFile(t, filepath.Join(scratchDir, ".wh.old.txt"), "")

	// Build an in-memory base tree with README.md, old.txt, and a node_modules subtree
	// so we can assert node_modules subtree is NEVER fetched when skipped via filepath.SkipDir.
	nodeModulesSubHash := bytes.Repeat([]byte{0xaa}, 32)
	srcSubEntries := []tree.Entry{
		{Name: "existing.go", Mode: 0100644, Size: 10, BlobSHA256: bytes.Repeat([]byte{0x11}, 32)},
	}
	srcSubHash := tree.ComputeTreeHash(srcSubEntries)

	rootEntries := []tree.Entry{
		{Name: "README.md", Mode: 0100644, Size: 5, BlobSHA256: bytes.Repeat([]byte{0x22}, 32)},
		{Name: "node_modules", Mode: 0040755, SubTreeHash: nodeModulesSubHash},
		{Name: "old.txt", Mode: 0100644, Size: 3, BlobSHA256: bytes.Repeat([]byte{0x33}, 32)},
		{Name: "src", Mode: 0040755, SubTreeHash: srcSubHash},
	}
	tree.SortEntries(rootEntries)
	rootHash := tree.ComputeTreeHash(rootEntries)

	fetchedTrees := make(map[string]int)
	fetchTree := func(hash []byte) ([]tree.Entry, error) {
		key := hex.EncodeToString(hash)
		fetchedTrees[key]++
		if bytes.Equal(hash, rootHash) {
			return rootEntries, nil
		}
		if bytes.Equal(hash, srcSubHash) {
			return srcSubEntries, nil
		}
		if bytes.Equal(hash, nodeModulesSubHash) {
			t.Errorf("fetchTree was unexpectedly called for ignored directory subtree node_modules")
		}
		return nil, nil
	}

	changes, err := ScanScratchChanges(scratchDir, rootHash, fetchTree)
	if err != nil {
		t.Fatalf("ScanScratchChanges failed: %v", err)
	}

	want := map[string]ChangeType{
		".nieblaignore": ChangeAdded,
		"README.md":     ChangeModified,
		"important.log": ChangeAdded,
		"old.txt":       ChangeDeleted,
		"src/main.go":   ChangeAdded,
	}

	if len(changes) != len(want) {
		t.Fatalf("got %d changes (%+v), want %d (%+v)", len(changes), changes, len(want), want)
	}

	for _, c := range changes {
		expectedType, ok := want[c.Path]
		if !ok {
			t.Errorf("unexpected file change reported: %s (%s)", c.Path, c.Type)
			continue
		}
		if c.Type != expectedType {
			t.Errorf("change %s: got type %s, want %s", c.Path, c.Type, expectedType)
		}
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func TestScanScratchChanges_TracksDotNieblaGovernance(t *testing.T) {
	tempDir := t.TempDir()
	scratchDir := filepath.Join(tempDir, "scratch")
	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		t.Fatalf("failed to create scratch dir: %v", err)
	}

	// 1. Create governance files inside .niebla directory
	ownersContent := `approvers = ["alice", "bob"]
required_approvals = 1
`
	mustWriteFile(t, filepath.Join(scratchDir, ".niebla", "OWNERS.toml"), ownersContent)

	agentsContent := `[[agents]]
name = "sec-scanner"
cmd = "sec-scanner.sh"
`
	mustWriteFile(t, filepath.Join(scratchDir, ".niebla", "agents.toml"), agentsContent)

	hooksContent := `[[hooks]]
name = "no-raw-sql"
`
	mustWriteFile(t, filepath.Join(scratchDir, ".niebla", "hooks.toml"), hooksContent)

	promptContent := `# Security Review Persona
Look for SQL injections.
`
	mustWriteFile(t, filepath.Join(scratchDir, ".niebla", "prompts", "security.md"), promptContent)

	// Also create a daemon socket file (should be ignored)
	mustWriteFile(t, filepath.Join(scratchDir, "nieblafs.sock"), "")

	// Base tree is empty
	mockFetch := func(h []byte) ([]tree.Entry, error) {
		return []tree.Entry{}, nil
	}

	changes, err := ScanScratchChanges(scratchDir, nil, mockFetch)
	if err != nil {
		t.Fatalf("ScanScratchChanges failed: %v", err)
	}

	changeTypes := make(map[string]ChangeType)
	for _, c := range changes {
		changeTypes[c.Path] = c.Type
	}

	expectedTracked := []string{
		".niebla/OWNERS.toml",
		".niebla/agents.toml",
		".niebla/hooks.toml",
		".niebla/prompts/security.md",
	}

	for _, p := range expectedTracked {
		if changeTypes[p] != ChangeAdded {
			t.Errorf("expected %s to have ChangeAdded ([A]), got %v (all: %+v)", p, changeTypes[p], changeTypes)
		}
	}

	if _, hasSocket := changeTypes["nieblafs.sock"]; hasSocket {
		t.Errorf("nieblafs.sock should have been ignored by built-in matcher, but was returned in changes")
	}
}
