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

func TestParseTOML_SubtreeGovernanceAndPerFileTables(t *testing.T) {
	tomlData := []byte(`
set_noparent = false
approvers = ["alice@corp", "@security-leads"]
reviewers = ["bob@corp"]
required_approvals = 2
require_green_ci = true
allow_unresolved_comments = false
agents = ["sec-bot", "  lint-bot  "]
presubmit_hooks = ["no-raw-sql", "fips-crypto-check"]
ci_runners = ["security-integration-suite"]

[per_file]
"*.templ" = { approvers = ["@design-system"], reviewers = ["ui-reviewer"], set_noparent = true, required_approvals = 3, agents = ["a11y-bot"], presubmit_hooks = ["templ-lint"], ci_runners = ["ui-e2e"] }
"*.md"    = ["docs-team@corp"]
`)

	cfg, err := ParseTOML(tomlData)
	if err != nil {
		t.Fatalf("ParseTOML failed: %v", err)
	}

	if cfg.RequiredApprovals == nil || *cfg.RequiredApprovals != 2 {
		t.Errorf("expected required_approvals = 2, got %v", cfg.RequiredApprovals)
	}
	if cfg.RequireGreenCI == nil || !*cfg.RequireGreenCI {
		t.Errorf("expected require_green_ci = true, got %v", cfg.RequireGreenCI)
	}
	if cfg.AllowUnresolvedComments == nil || *cfg.AllowUnresolvedComments {
		t.Errorf("expected allow_unresolved_comments = false, got %v", cfg.AllowUnresolvedComments)
	}
	if len(cfg.Agents) != 2 || cfg.Agents[0] != "sec-bot" || cfg.Agents[1] != "lint-bot" {
		t.Errorf("unexpected agents: %v", cfg.Agents)
	}
	if len(cfg.PresubmitHooks) != 2 || cfg.PresubmitHooks[0] != "no-raw-sql" {
		t.Errorf("unexpected presubmit_hooks: %v", cfg.PresubmitHooks)
	}
	if len(cfg.CIRunners) != 1 || cfg.CIRunners[0] != "security-integration-suite" {
		t.Errorf("unexpected ci_runners: %v", cfg.CIRunners)
	}

	// Backward-compatible PerFile map still populated for both table and slice entries
	if len(cfg.PerFile["*.templ"]) != 1 || cfg.PerFile["*.templ"][0] != "@design-system" {
		t.Errorf("unexpected PerFile[*.templ]: %v", cfg.PerFile["*.templ"])
	}
	if len(cfg.PerFile["*.md"]) != 1 || cfg.PerFile["*.md"][0] != "docs-team@corp" {
		t.Errorf("unexpected PerFile[*.md]: %v", cfg.PerFile["*.md"])
	}

	// Rich PerFileRules populated
	templRule, ok := cfg.PerFileRules["*.templ"]
	if !ok {
		t.Fatalf("expected *.templ in PerFileRules")
	}
	if !templRule.SetNoParent {
		t.Errorf("expected *.templ SetNoParent = true")
	}
	if templRule.RequiredApprovals == nil || *templRule.RequiredApprovals != 3 {
		t.Errorf("expected *.templ RequiredApprovals = 3, got %v", templRule.RequiredApprovals)
	}
	if len(templRule.Agents) != 1 || templRule.Agents[0] != "a11y-bot" {
		t.Errorf("expected *.templ Agents = [a11y-bot], got %v", templRule.Agents)
	}
	if len(templRule.PresubmitHooks) != 1 || templRule.PresubmitHooks[0] != "templ-lint" {
		t.Errorf("expected *.templ PresubmitHooks = [templ-lint], got %v", templRule.PresubmitHooks)
	}
	if len(templRule.CIRunners) != 1 || templRule.CIRunners[0] != "ui-e2e" {
		t.Errorf("expected *.templ CIRunners = [ui-e2e], got %v", templRule.CIRunners)
	}
}

func TestEvaluator_MultiSubtreePolicyAggregation(t *testing.T) {
	files := map[string]string{
		"": `
approvers = ["root-admin"]
required_approvals = 1
require_green_ci = false
allow_unresolved_comments = true
agents = ["global-bot"]
presubmit_hooks = ["conventional-commits"]
`,
		"internal/web/views": `
approvers = ["ui-lead"]
reviewers = ["ux-reviewer"]
agents = ["ui-bot"]
[per_file]
"*.templ" = { approvers = ["design-lead"], agents = ["a11y-bot"], presubmit_hooks = ["templ-check"] }
`,
		"pkg/auth": `
set_noparent = true
approvers = ["sec-lead"]
required_approvals = 2
allow_unresolved_comments = false
agents = ["sec-bot"]
presubmit_hooks = ["fips-crypto-check"]
ci_runners = ["security-suite"]
`,
	}

	provider := func(dir string) ([]byte, error) {
		if content, ok := files[dir]; ok {
			return []byte(content), nil
		}
		return nil, os.ErrNotExist
	}

	evaluator := NewEvaluator(provider, nil)

	// Case 1: Touching only pkg/auth/token.go (set_noparent = true isolates from root)
	resAuth, err := evaluator.Evaluate([]string{"pkg/auth/token.go"}, []string{"sec-lead"})
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if !resAuth.Satisfied {
		t.Errorf("expected sec-lead to satisfy pkg/auth/token.go")
	}
	if resAuth.Policy.RequiredApprovals == nil || *resAuth.Policy.RequiredApprovals != 2 {
		t.Errorf("expected RequiredApprovals = 2, got %v", resAuth.Policy.RequiredApprovals)
	}
	if resAuth.Policy.RequireGreenCI == nil || !*resAuth.Policy.RequireGreenCI {
		t.Errorf("expected RequireGreenCI = true (implied by ci_runners), got %v", resAuth.Policy.RequireGreenCI)
	}
	if resAuth.Policy.AllowUnresolvedComments == nil || *resAuth.Policy.AllowUnresolvedComments {
		t.Errorf("expected AllowUnresolvedComments = false, got %v", resAuth.Policy.AllowUnresolvedComments)
	}
	// Because pkg/auth sets set_noparent = true, global-bot and conventional-commits from root should NOT be inherited
	if len(resAuth.Policy.Agents) != 1 || resAuth.Policy.Agents[0] != "sec-bot" {
		t.Errorf("expected only [sec-bot] due to set_noparent, got %v", resAuth.Policy.Agents)
	}
	if len(resAuth.Policy.PresubmitHooks) != 1 || resAuth.Policy.PresubmitHooks[0] != "fips-crypto-check" {
		t.Errorf("expected only [fips-crypto-check] due to set_noparent, got %v", resAuth.Policy.PresubmitHooks)
	}

	// Case 2: Cross-subtree CL touching both internal/web/views/modal.templ AND pkg/auth/token.go
	resMulti, err := evaluator.Evaluate(
		[]string{"internal/web/views/modal.templ", "pkg/auth/token.go"},
		[]string{"design-lead", "sec-lead"},
	)
	if err != nil {
		t.Fatalf("Evaluate multi-subtree failed: %v", err)
	}
	if !resMulti.Satisfied {
		t.Errorf("expected design-lead + sec-lead to satisfy multi-subtree CL, missing: %+v", resMulti.Missing)
	}
	// max(1 from root via views, 2 from pkg/auth) == 2
	if resMulti.Policy.RequiredApprovals == nil || *resMulti.Policy.RequiredApprovals != 2 {
		t.Errorf("expected aggregated RequiredApprovals = 2, got %v", resMulti.Policy.RequiredApprovals)
	}
	// strictest allow_unresolved_comments (true from root vs false from pkg/auth) == false
	if resMulti.Policy.AllowUnresolvedComments == nil || *resMulti.Policy.AllowUnresolvedComments {
		t.Errorf("expected aggregated AllowUnresolvedComments = false, got %v", resMulti.Policy.AllowUnresolvedComments)
	}
	// union of agents: a11y-bot (*.templ), ui-bot (views), global-bot (root via views), sec-bot (pkg/auth)
	expectedAgents := map[string]bool{"a11y-bot": true, "ui-bot": true, "global-bot": true, "sec-bot": true}
	if len(resMulti.Policy.Agents) != 4 {
		t.Errorf("expected 4 aggregated agents, got %v", resMulti.Policy.Agents)
	}
	for _, a := range resMulti.Policy.Agents {
		if !expectedAgents[a] {
			t.Errorf("unexpected agent %q in %v", a, resMulti.Policy.Agents)
		}
	}
}
