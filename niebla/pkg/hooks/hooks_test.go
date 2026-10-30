package hooks

import (
	"testing"
)

func TestParseTOML(t *testing.T) {
	rawTOML := `
[[client.presubmit]]
name = "check-fmt"
command = "gofmt -l ."
fail_on_output = true
description = "Verify Go files are formatted"

[[server.presubmit]]
name = "conventional-commits"
type = "metadata"
blocking = true
description = "Enforce Conventional Commits"
[server.presubmit.metadata]
field = "description"
pattern = "^(feat|fix|docs|refactor|test|chore)(\\(.+\\))?: .+"
min_length = 10

[[server.presubmit]]
name = "forbid-secrets"
type = "tree_scan"
description = "Block credentials and huge files"
[server.presubmit.tree_scan]
forbidden_patterns = ["*.pem", "*.key", "id_rsa*"]
max_file_size_bytes = 10485760

[[server.presubmit]]
name = "advisory-linter"
type = "cas_binary"
blocking = false
cas_sha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
args = ["--warn-only"]
`

	cfg, err := ParseTOML([]byte(rawTOML))
	if err != nil {
		t.Fatalf("failed to parse TOML: %v", err)
	}

	if len(cfg.Client.Presubmit) != 1 {
		t.Fatalf("expected 1 client presubmit, got %d", len(cfg.Client.Presubmit))
	}
	if cfg.Client.Presubmit[0].Name != "check-fmt" || !cfg.Client.Presubmit[0].FailOnOutput {
		t.Errorf("unexpected client hook: %+v", cfg.Client.Presubmit[0])
	}

	if len(cfg.Server.Presubmit) != 3 {
		t.Fatalf("expected 3 server presubmits, got %d", len(cfg.Server.Presubmit))
	}

	// 1. Explicit blocking = true
	if !cfg.Server.Presubmit[0].IsBlocking() {
		t.Errorf("expected hook 0 to be blocking")
	}

	// 2. Default blocking (omitted in TOML)
	if !cfg.Server.Presubmit[1].IsBlocking() {
		t.Errorf("expected hook 1 to default to blocking when omitted")
	}

	// 3. Explicit blocking = false
	if cfg.Server.Presubmit[2].IsBlocking() {
		t.Errorf("expected hook 2 to be non-blocking")
	}
}

func TestParseTOML_Invalid(t *testing.T) {
	_, err := ParseTOML([]byte("invalid [toml = syntax"))
	if err == nil {
		t.Errorf("expected error for invalid TOML syntax")
	}
}

func TestEvaluateMetadata(t *testing.T) {
	hook := ServerHook{
		Name: "commit-checker",
		Type: "metadata",
		Metadata: &MetadataRule{
			Field:     "description",
			Pattern:   "^(feat|fix|docs)(\\(.+\\))?: .+",
			MinLength: 10,
			MaxLength: 100,
		},
	}

	// 1. Valid metadata passes
	metaValid := CLMetadata{
		Author:      "alice",
		Description: "feat(auth): implement Bearer token check",
	}
	if fails := EvaluateMetadata(hook, metaValid); len(fails) > 0 {
		t.Errorf("expected valid description to pass, got failures: %+v", fails)
	}

	// 2. Fails regex
	metaInvalidRegex := CLMetadata{
		Author:      "alice",
		Description: "wip: just fixing things",
	}
	fails := EvaluateMetadata(hook, metaInvalidRegex)
	if len(fails) != 1 || fails[0].HookName != "commit-checker" {
		t.Errorf("expected regex failure, got: %+v", fails)
	}

	// 3. Fails min length
	metaTooShort := CLMetadata{
		Author:      "alice",
		Description: "fix: ok",
	}
	fails = EvaluateMetadata(hook, metaTooShort)
	if len(fails) == 0 {
		t.Errorf("expected min length failure")
	}

	// 4. Author validation
	authorHook := ServerHook{
		Name: "corp-email-checker",
		Type: "metadata",
		Metadata: &MetadataRule{
			Field:   "author",
			Pattern: "^.+@corp\\.internal$",
		},
	}
	if fails := EvaluateMetadata(authorHook, CLMetadata{Author: "alice@corp.internal"}); len(fails) > 0 {
		t.Errorf("expected valid author to pass, got: %+v", fails)
	}
	if fails := EvaluateMetadata(authorHook, CLMetadata{Author: "hacker@external.net"}); len(fails) == 0 {
		t.Errorf("expected invalid author to fail")
	}
}

func TestEvaluateTreeScan(t *testing.T) {
	hook := ServerHook{
		Name: "secret-and-size-guard",
		Type: "tree_scan",
		TreeScan: &TreeScanRule{
			ForbiddenPatterns: []string{"*.pem", "*.key", "id_rsa*", "secrets/*"},
			MaxFileSizeBytes:  1024 * 1024, // 1MB
		},
	}

	// 1. Clean files pass
	cleanFiles := []TouchedFile{
		{Path: "src/main.go", SizeBytes: 500, Status: "modified"},
		{Path: "docs/readme.md", SizeBytes: 2048, Status: "added"},
	}
	if fails := EvaluateTreeScan(hook, cleanFiles); len(fails) > 0 {
		t.Errorf("expected clean files to pass, got: %+v", fails)
	}

	// 2. Forbidden patterns detected
	badFiles := []TouchedFile{
		{Path: "certs/ca.pem", SizeBytes: 1200, Status: "added"},
		{Path: "config/prod.key", SizeBytes: 800, Status: "modified"},
		{Path: "ssh/id_rsa", SizeBytes: 1600, Status: "added"},
		{Path: "secrets/credentials.json", SizeBytes: 300, Status: "added"},
	}
	fails := EvaluateTreeScan(hook, badFiles)
	if len(fails) != 4 {
		t.Errorf("expected 4 forbidden pattern failures, got %d: %+v", len(fails), fails)
	}

	// 3. Max file size exceeded
	hugeFiles := []TouchedFile{
		{Path: "bigdata/dataset.bin", SizeBytes: 5 * 1024 * 1024, Status: "added"}, // 5MB > 1MB
	}
	fails = EvaluateTreeScan(hook, hugeFiles)
	if len(fails) != 1 || fails[0].Path != "bigdata/dataset.bin" {
		t.Errorf("expected file size failure, got: %+v", fails)
	}

	// 4. Deleted files with forbidden names or large sizes are ignored
	deletedFiles := []TouchedFile{
		{Path: "certs/old.pem", SizeBytes: 20 * 1024 * 1024, Status: "deleted"},
	}
	if fails := EvaluateTreeScan(hook, deletedFiles); len(fails) > 0 {
		t.Errorf("expected deleted files to be skipped, got: %+v", fails)
	}
}

func TestEvaluateServerPresubmit_BlockingVsNonBlocking(t *testing.T) {
	nonBlocking := false
	blockingHooks := []ServerHook{
		{
			Name: "blocking-metadata",
			Type: "metadata",
			Metadata: &MetadataRule{
				Field:   "description",
				Pattern: "^feat: .+",
			},
		},
		{
			Name:     "advisory-tree",
			Type:     "tree_scan",
			Blocking: &nonBlocking,
			TreeScan: &TreeScanRule{
				ForbiddenPatterns: []string{"*.log"},
			},
		},
	}

	// Case 1: All pass
	res := EvaluateServerPresubmit(blockingHooks, CLMetadata{Description: "feat: valid feature"}, []TouchedFile{
		{Path: "src/app.go", Status: "modified"},
	})
	if !res.Passed || len(res.Failures) > 0 {
		t.Errorf("expected clean pass, got: %+v", res)
	}

	// Case 2: Only non-blocking advisory hook fails -> overall Passed is still TRUE
	res = EvaluateServerPresubmit(blockingHooks, CLMetadata{Description: "feat: valid feature"}, []TouchedFile{
		{Path: "debug.log", Status: "added"},
	})
	if !res.Passed {
		t.Errorf("expected Passed=true when only non-blocking hook fails")
	}
	if len(res.Failures) != 1 || res.Failures[0].HookName != "advisory-tree" {
		t.Errorf("expected 1 advisory failure recorded, got: %+v", res.Failures)
	}

	// Case 3: Blocking hook fails -> overall Passed is FALSE
	res = EvaluateServerPresubmit(blockingHooks, CLMetadata{Description: "bad description"}, []TouchedFile{
		{Path: "src/app.go", Status: "modified"},
	})
	if res.Passed {
		t.Errorf("expected Passed=false when blocking hook fails")
	}
}
