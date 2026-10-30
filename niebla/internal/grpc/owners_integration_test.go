package grpc

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"niebla/internal/config"
	"niebla/internal/db"
	"niebla/pkg/cas"
	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type dirNode struct {
	files   map[string][]byte
	subdirs map[string]*dirNode
}

func putHierarchicalTree(t *testing.T, ctx context.Context, database *db.DB, casStorage *cas.Storage, files map[string]string) []byte {
	t.Helper()
	root := &dirNode{files: make(map[string][]byte), subdirs: make(map[string]*dirNode)}

	for fullPath, content := range files {
		clean := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(fullPath)), "/")
		parts := strings.Split(clean, "/")
		curr := root
		for i := 0; i < len(parts)-1; i++ {
			p := parts[i]
			if _, ok := curr.subdirs[p]; !ok {
				curr.subdirs[p] = &dirNode{files: make(map[string][]byte), subdirs: make(map[string]*dirNode)}
			}
			curr = curr.subdirs[p]
		}
		curr.files[parts[len(parts)-1]] = []byte(content)
	}

	var persistNode func(n *dirNode) []byte
	persistNode = func(n *dirNode) []byte {
		var entries []tree.Entry
		for fName, data := range n.files {
			hashBytes, _, size, storagePath, err := casStorage.Put(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("cas.Put failed: %v", err)
			}
			_ = database.InsertBlob(ctx, hashBytes, size, storagePath)
			entries = append(entries, tree.Entry{
				Name:       fName,
				Mode:       0100644,
				Size:       uint64(size),
				BlobSHA256: hashBytes,
			})
		}
		for subName, subNode := range n.subdirs {
			subHash := persistNode(subNode)
			entries = append(entries, tree.Entry{
				Name:        subName,
				Mode:        0040755,
				SubTreeHash: subHash,
			})
		}
		tree.SortEntries(entries)
		treeHash := tree.ComputeTreeHash(entries)
		if err := database.InsertTreeNodes(ctx, treeHash, entries); err != nil {
			t.Fatalf("InsertTreeNodes failed: %v", err)
		}
		return treeHash
	}

	return persistNode(root)
}

func TestOwnersIntegration_MultiDirectoryAndPerFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers integration test in short mode")
	}

	client, database, casStorage, server, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Configure static groups for enterprise group resolution
	cfg := config.DefaultConfig()
	cfg.Critique.Groups = config.CritiqueGroupConfig{
		Mode: "static",
		Static: map[string][]string{
			"sec-team": {"eve", "frank"},
		},
	}
	server.SetToolchainConfig(cfg)

	ctx := context.Background()

	// 1. Establish base trunk head with hierarchical OWNERS.toml files
	// - Root OWNERS.toml: approvers = ["root-admin"]
	// - pkg/security/OWNERS.toml: set_noparent = true, approvers = ["@sec-team"]
	// - per-file "*.key" = ["ciso"]
	baseTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
		"OWNERS.toml": `
approvers = ["root-admin"]
`,
		"pkg/security/OWNERS.toml": `
set_noparent = true
approvers = ["@sec-team"]

[per_file]
"*.key" = ["ciso"]
`,
		"pkg/security/auth.go":    "package security\n",
		"pkg/security/secret.key": "dummy-key\n",
		"cmd/app/main.go":         "package main\n",
	})

	baseResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		Author:       "root-admin",
		Description:  "initial trunk",
		RootTreeHash: baseTree,
	})
	if err != nil {
		t.Fatalf("failed to shelve base CL: %v", err)
	}

	// Submit base trunk CL with Force=true to establish baseline trunk revision
	_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: baseResp.ClId, Force: true})
	if err != nil {
		t.Fatalf("failed to establish baseline trunk: %v", err)
	}

	// --- Case 1: Subsystem with set_noparent requires subsystem owner, root admin is rejected ---
	t.Run("set_noparent_blocks_root_admin", func(t *testing.T) {
		clTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
			"OWNERS.toml": `
approvers = ["root-admin"]
`,
			"pkg/security/OWNERS.toml": `
set_noparent = true
approvers = ["@sec-team"]

[per_file]
"*.key" = ["ciso"]
`,
			"pkg/security/auth.go":    "package security\n// patch\n",
			"pkg/security/secret.key": "dummy-key\n",
			"cmd/app/main.go":         "package main\n",
		})

		clResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
			Author:       "dev",
			Description:  "auth patch",
			ParentCl:     baseResp.ClId,
			RootTreeHash: clTree,
		})
		if err != nil {
			t.Fatalf("failed to shelve: %v", err)
		}

		// Root admin approves -> should STILL be blocked because pkg/security has set_noparent = true!
		_ = database.SetApproval(ctx, clResp.ClId, "root-admin", "lgtm")

		_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clResp.ClId})
		if err == nil {
			t.Fatalf("expected submit to be blocked by set_noparent, but succeeded")
		}
		if status.Code(err) != codes.FailedPrecondition {
			t.Errorf("expected FailedPrecondition, got: %v", status.Code(err))
		}

		// Member of @sec-team ("eve") approves -> succeeds!
		_ = database.SetApproval(ctx, clResp.ClId, "eve", "lgtm")
		submitResp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clResp.ClId})
		if err != nil {
			t.Fatalf("expected submit to succeed after @sec-team approval, got: %v", err)
		}
		if submitResp.ClId != clResp.ClId {
			t.Errorf("expected CL %d, got %d", clResp.ClId, submitResp.ClId)
		}
	})

	// --- Case 2: Per-file rule takes precedence over directory approver ---
	t.Run("per_file_rule_requires_specific_owner", func(t *testing.T) {
		clTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
			"OWNERS.toml": `
approvers = ["root-admin"]
`,
			"pkg/security/OWNERS.toml": `
set_noparent = true
approvers = ["@sec-team"]

[per_file]
"*.key" = ["ciso"]
`,
			"pkg/security/auth.go":    "package security\n// patch\n",
			"pkg/security/secret.key": "dummy-key-rotated\n",
			"cmd/app/main.go":         "package main\n",
		})

		headCL, err := database.GetHeadCL(ctx)
		if err != nil {
			t.Fatalf("failed to get head CL: %v", err)
		}

		clResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
			Author:       "dev",
			Description:  "key rotation",
			ParentCl:     headCL.CLID,
			RootTreeHash: clTree,
		})
		if err != nil {
			t.Fatalf("failed to shelve: %v", err)
		}

		// eve (@sec-team) approves -> directory owner, but *.key requires "ciso"
		_ = database.SetApproval(ctx, clResp.ClId, "eve", "lgtm")
		_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clResp.ClId})
		if err == nil {
			t.Fatalf("expected submit to be blocked by per-file rule, but succeeded")
		}

		// ciso approves -> satisfies *.key!
		_ = database.SetApproval(ctx, clResp.ClId, "ciso", "lgtm")
		_, err = client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{ClId: clResp.ClId})
		if err != nil {
			t.Fatalf("expected submit to succeed after ciso approval, got: %v", err)
		}
	})

	// --- Case 3: Emergency bypass with Force=true ---
	t.Run("emergency_force_bypass", func(t *testing.T) {
		headCL, err := database.GetHeadCL(ctx)
		if err != nil {
			t.Fatalf("failed to get head CL: %v", err)
		}

		clTree := putHierarchicalTree(t, ctx, database, casStorage, map[string]string{
			"OWNERS.toml": `
approvers = ["root-admin"]
`,
			"pkg/security/OWNERS.toml": `
set_noparent = true
approvers = ["@sec-team"]
`,
			"pkg/security/auth.go":    "package security\n// patch\n",
			"pkg/security/secret.key": "dummy-key-rotated\n",
			"cmd/app/main.go":         "package main\n// emergency hotfix\n",
		})

		clResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
			Author:       "oncall",
			Description:  "emergency hotfix",
			ParentCl:     headCL.CLID,
			RootTreeHash: clTree,
		})
		if err != nil {
			t.Fatalf("failed to shelve: %v", err)
		}

		// Submit with Force = true without any approvals -> succeeds!
		resp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{
			ClId:  clResp.ClId,
			Force: true,
		})
		if err != nil {
			t.Fatalf("expected Force=true to bypass OWNERS, got: %v", err)
		}
		if resp.ClId != clResp.ClId {
			t.Errorf("expected CL %d, got %d", clResp.ClId, resp.ClId)
		}
	})
}
