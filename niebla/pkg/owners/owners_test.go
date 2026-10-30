package owners

import (
	"os"
	"testing"
)

func TestParseTOML(t *testing.T) {
	tomlData := []byte(`
set_noparent = true

approvers = ["alice", "bob", "  @tech-leads  "]
reviewers = ["charlie"]

[per_file]
"*.proto" = ["@api-governance", "grace"]
"*.sql"   = ["dba-lead"]
`)

	cfg, err := ParseTOML(tomlData)
	if err != nil {
		t.Fatalf("ParseTOML failed: %v", err)
	}

	if !cfg.SetNoParent {
		t.Errorf("expected SetNoParent = true")
	}

	if len(cfg.Approvers) != 3 || cfg.Approvers[2] != "@tech-leads" {
		t.Errorf("unexpected approvers: %v", cfg.Approvers)
	}

	if len(cfg.Reviewers) != 1 || cfg.Reviewers[0] != "charlie" {
		t.Errorf("unexpected reviewers: %v", cfg.Reviewers)
	}

	if len(cfg.PerFile) != 2 {
		t.Fatalf("expected 2 per_file rules, got %d", len(cfg.PerFile))
	}
	if len(cfg.PerFile["*.proto"]) != 2 || cfg.PerFile["*.proto"][1] != "grace" {
		t.Errorf("unexpected per_file *.proto rule: %v", cfg.PerFile["*.proto"])
	}
}

func TestEvaluator_HierarchyAndNoParent(t *testing.T) {
	// Virtual repository layout:
	// /OWNERS.toml: approvers = ["root-admin"]
	// /pkg/OWNERS.toml: approvers = ["pkg-lead"]
	// /pkg/auth/OWNERS.toml: set_noparent = true, approvers = ["auth-owner"]
	// /pkg/auth/api/ (no OWNERS.toml)

	files := map[string]string{
		"": `
approvers = ["root-admin"]
`,
		"pkg": `
approvers = ["pkg-lead"]
`,
		"pkg/auth": `
set_noparent = true
approvers = ["auth-owner"]
[per_file]
"*.proto" = ["api-specialist"]
`,
	}

	provider := func(dir string) ([]byte, error) {
		if content, ok := files[dir]; ok {
			return []byte(content), nil
		}
		return nil, os.ErrNotExist
	}

	evaluator := NewEvaluator(provider, nil)

	// 1. Root file: "README.md" -> governed by root-admin
	reqs, err := evaluator.ResolveRequirements([]string{"README.md"})
	if err != nil {
		t.Fatalf("ResolveRequirements failed: %v", err)
	}
	if len(reqs) != 1 || len(reqs[0].Approvers) != 1 || reqs[0].Approvers[0] != "root-admin" {
		t.Errorf("expected root-admin for README.md, got: %+v", reqs)
	}

	// 2. Child without set_noparent: "pkg/util.go" -> inherits pkg-lead and root-admin
	reqs, err = evaluator.ResolveRequirements([]string{"pkg/util.go"})
	if err != nil {
		t.Fatalf("ResolveRequirements failed: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("expected 1 requirement, got %d", len(reqs))
	}
	approverSet := make(map[string]bool)
	for _, a := range reqs[0].Approvers {
		approverSet[a] = true
	}
	if !approverSet["pkg-lead"] || !approverSet["root-admin"] {
		t.Errorf("expected pkg-lead and root-admin, got: %v", reqs[0].Approvers)
	}

	// 3. Subsystem with set_noparent = true: "pkg/auth/token.go" -> only auth-owner
	reqs, err = evaluator.ResolveRequirements([]string{"pkg/auth/token.go"})
	if err != nil {
		t.Fatalf("ResolveRequirements failed: %v", err)
	}
	if len(reqs) != 1 || len(reqs[0].Approvers) != 1 || reqs[0].Approvers[0] != "auth-owner" {
		t.Errorf("expected only auth-owner due to set_noparent, got: %+v", reqs)
	}

	// 4. Per-file rule match: "pkg/auth/service.proto" -> api-specialist
	reqs, err = evaluator.ResolveRequirements([]string{"pkg/auth/service.proto"})
	if err != nil {
		t.Fatalf("ResolveRequirements failed: %v", err)
	}
	if len(reqs) != 1 || len(reqs[0].Approvers) != 1 || reqs[0].Approvers[0] != "api-specialist" {
		t.Errorf("expected api-specialist for proto, got: %+v", reqs)
	}
}

func TestEvaluator_EvaluateApprovalsAndGroups(t *testing.T) {
	files := map[string]string{
		"": `
approvers = ["root-admin"]
`,
		"backend": `
set_noparent = true
approvers = ["@backend-team"]
`,
	}

	groupResolver := func(group string) []string {
		if group == "@backend-team" {
			return []string{"alice", "bob"}
		}
		return nil
	}

	provider := func(dir string) ([]byte, error) {
		if content, ok := files[dir]; ok {
			return []byte(content), nil
		}
		return nil, os.ErrNotExist
	}

	evaluator := NewEvaluator(provider, groupResolver)

	touched := []string{"backend/server.go", "README.md"}

	// 1. No approvals granted -> Unsatisfied
	res, err := evaluator.Evaluate(touched, nil)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if res.Satisfied || len(res.Missing) != 2 {
		t.Errorf("expected 2 missing requirements, got satisfied=%v, missing=%d", res.Satisfied, len(res.Missing))
	}

	// 2. Only alice (@backend-team) approved -> README.md still missing
	res, err = evaluator.Evaluate(touched, []string{"alice"})
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if res.Satisfied || len(res.Missing) != 1 || res.Missing[0].Path != "README.md" {
		t.Errorf("expected README.md missing, got: %+v", res)
	}
	if len(res.Approved) != 1 || res.Approved[0].Path != "backend/server.go" {
		t.Errorf("expected backend/server.go approved by alice, got: %+v", res.Approved)
	}

	// 3. Both alice and root-admin approved -> Satisfied!
	res, err = evaluator.Evaluate(touched, []string{"alice", "root-admin"})
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if !res.Satisfied || len(res.Missing) != 0 || len(res.Approved) != 2 {
		t.Errorf("expected full approval satisfaction, got: %+v", res)
	}

	// 4. Case-insensitivity check: "ALICE" and "Root-Admin"
	res, err = evaluator.Evaluate(touched, []string{"ALICE", "Root-Admin"})
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if !res.Satisfied {
		t.Errorf("expected case-insensitive matching to succeed")
	}
}

func TestEvaluator_UnconstrainedRepo(t *testing.T) {
	// If no OWNERS.toml exists anywhere in the repository, changes are unconstrained
	emptyProvider := func(dir string) ([]byte, error) {
		return nil, os.ErrNotExist
	}

	evaluator := NewEvaluator(emptyProvider, nil)
	res, err := evaluator.Evaluate([]string{"src/main.go", "docs/index.md"}, nil)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if !res.Satisfied || len(res.Missing) != 0 {
		t.Errorf("expected unconstrained repo to be satisfied by default")
	}
}
