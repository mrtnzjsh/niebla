package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"niebla/internal/db"
	"niebla/pkg/cas"
	"niebla/pkg/tree"
)

type dirNode struct {
	files   map[string][]byte
	subdirs map[string]*dirNode
}

func putTree(ctx context.Context, database *db.DB, casStorage *cas.Storage, files map[string]string) []byte {
	root := &dirNode{files: make(map[string][]byte), subdirs: make(map[string]*dirNode)}

	for clean, content := range files {
		clean = filepath.ToSlash(filepath.Clean(clean))
		var comps []string
		for _, comp := range bytes.Split([]byte(clean), []byte("/")) {
			if len(comp) > 0 {
				comps = append(comps, string(comp))
			}
		}

		curr := root
		for i := 0; i < len(comps)-1; i++ {
			part := comps[i]
			if _, ok := curr.subdirs[part]; !ok {
				curr.subdirs[part] = &dirNode{files: make(map[string][]byte), subdirs: make(map[string]*dirNode)}
			}
			curr = curr.subdirs[part]
		}
		curr.files[comps[len(comps)-1]] = []byte(content)
	}

	var persistNode func(n *dirNode) []byte
	persistNode = func(n *dirNode) []byte {
		var entries []tree.Entry
		for fName, data := range n.files {
			hashBytes, _, size, storagePath, err := casStorage.Put(bytes.NewReader(data))
			if err != nil {
				log.Fatalf("cas.Put failed: %v", err)
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
			log.Fatalf("InsertTreeNodes failed: %v", err)
		}
		return treeHash
	}

	return persistNode(root)
}

func main() {
	ctx := context.Background()
	connStr := "postgres://postgres:postgres@127.0.0.1:5432/niebla?sslmode=disable"
	database, err := db.Connect(ctx, db.DefaultConfig(connStr))
	if err != nil {
		log.Fatalf("Failed to connect to db: %v", err)
	}
	defer database.Close()
	if err := database.RunMigrations(ctx); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	home, _ := os.UserHomeDir()
	casDir := filepath.Join(home, ".cache", "niebla", "blobs")
	casStorage, err := cas.NewStorage(casDir)
	if err != nil {
		log.Fatalf("Failed to init cas: %v", err)
	}

	// Clean out any extra CLs and reset tables so CL 1 is our single, rich review changelist
	pool := database.Pool()
	_, _ = pool.Exec(ctx, "DELETE FROM cl_comments;")
	_, _ = pool.Exec(ctx, "DELETE FROM cl_check_runs;")
	_, _ = pool.Exec(ctx, "DELETE FROM cl_approvals;")
	_, _ = pool.Exec(ctx, "DELETE FROM cl_reviewers;")
	_, _ = pool.Exec(ctx, "DELETE FROM cl_file_reviews;")
	_, _ = pool.Exec(ctx, "DELETE FROM cl_patchsets;")
	_, _ = pool.Exec(ctx, "DELETE FROM changelists WHERE cl_id > 1;")

	filesPS1 := map[string]string{
		"README.md": "# Niebla Monorepo\n\nCloud-native monorepo VCS and Cotejo code review platform.\n\n## Features\n- Virtual FUSE & FUSE-less workspaces\n- Server-authoritative Merkle trees\n",
		"cmd/app/main.go": `package main

import (
	"context"
	"fmt"
	"time"
)

// Server handles monorepo transactions.
type Server struct {
	port int
}

func main() {
	fmt.Println("Niebla Unified Toolchain v1.0")
	srv := &Server{port: 7331}
	fmt.Printf("Cotejo listening on :%d\n", srv.port)
}
`,
		"pkg/auth/token.go": `package auth

import (
	"errors"
	"time"
)

// ValidateToken checks if the bearer token is valid.
func ValidateToken(token string) (bool, error) {
	if token == "" {
		return false, errors.New("empty bearer token")
	}
	return true, nil
}
`,
		".niebla/hooks.toml": `[[client.presubmit]]
name = "gofmt"
command = "gofmt -l ."
fail_on_output = true

[[server.presubmit]]
name = "conventional-commits"
type = "metadata"
blocking = true
[server.presubmit.metadata]
field = "description"
pattern = "^(feat|fix|docs): .+"
`,
		".niebla/agents.toml": `[[agent]]
name = "package-builder"
display_name = "Package Build Runner"
type = "in_tree"
entrypoint = "tools/agents/builder.sh"
trigger_paths = ["cmd/**", "pkg/**"]
auto_approve = true
capture_logs = true

[[agent]]
name = "sec-bot"
display_name = "Security Scanner"
type = "in_tree"
entrypoint = "tools/agents/sec-bot.sh"
trigger_paths = ["pkg/auth/**"]
auto_approve = true
capture_logs = true

[[agent]]
name = "ai-hint-agent"
display_name = "AI Code Review Hint Assistant"
type = "in_tree"
entrypoint = "tools/agents/ai-hint-agent/main.go"
args = ["--prompt", ".niebla/prompts/comment_hint.md"]
blocking = false
auto_approve = false
capture_logs = true
timeout_seconds = 30
`,
		"OWNERS.toml": `approvers = ["matatan", "@team-leads"]
reviewers = ["alice", "bob"]
`,
	}
	treePS1 := putTree(ctx, database, casStorage, filesPS1)

	filesPS2 := make(map[string]string)
	for filePath, content := range filesPS1 {
		filesPS2[filePath] = content
	}
	filesPS2["cmd/app/main.go"] = `package main

import (
	"context"
	"fmt"
	"time"
)

// Server handles monorepo transactions and reviews.
type Server struct {
	port    int
	version string
}

func main() {
	fmt.Println("Niebla Unified Toolchain v2.0")
	srv := &Server{port: 7331, version: "2.0.0"}
	fmt.Printf("Cotejo code review UI listening on :%d (v%s)\n", srv.port, srv.version)
}
`
	filesPS2["pkg/auth/token.go"] = `package auth

import (
	"errors"
	"time"
)

// ValidateToken checks if the bearer token is valid and unexpired.
func ValidateToken(token string) (bool, error) {
	if token == "" {
		return false, errors.New("empty bearer token")
	}
	// Verified token signature and TTL
	return true, nil
}
`
	treePS2 := putTree(ctx, database, casStorage, filesPS2)

	desc := `feat(cotejo): implement fast server-authoritative code review web UI and Myers diff engine

- Pure Go + a-h/templ + HTMX + Tailwind CSS
- Myers diff visualizer with Chroma syntax highlighting
- Side-by-side row alignment, patchset history, and inline commenting
- Submit guards for OWNERS, unresolved comments, and CI check runs`

	updateQuery := `
		UPDATE changelists
		SET author = 'matatan',
		    description = $1,
		    root_tree_hash = $2,
		    status = 'shelved',
		    parent_cl = NULL
		WHERE cl_id = 1;
	`
	if _, err := pool.Exec(ctx, updateQuery, desc, treePS2); err != nil {
		log.Fatalf("Failed to update CL 1: %v", err)
	}

	_ = database.CreatePatchset(ctx, 1, 1, treePS1)
	_ = database.CreatePatchset(ctx, 1, 2, treePS2)

	_ = database.AddReviewers(ctx, 1, []string{"matatan", "alice", "package-builder", "sec-bot"})
	_ = database.SetApproval(ctx, 1, "alice", "lgtm")
	_ = database.SetApproval(ctx, 1, "package-builder", "lgtm")
	_ = database.SetApproval(ctx, 1, "sec-bot", "lgtm")

	_, _ = database.CreateCommentWithSuggestion(ctx,
		1, 2, "pkg/auth/token.go", 10, 14, "modified", "sec-bot",
		"⚠️ Token expiration timestamp and minimum length validation recommended for production bearer auth.",
		nil, false, 10, 14,
		`	if len(token) < 32 {
		return false, errors.New("bearer token below minimum 32-byte length")
	}
	// Verified token signature and TTL
	return true, nil`,
	)
	_, _ = database.CreateComment(ctx, 1, 2, "cmd/app/main.go", 17, 18, "modified", "alice",
		"LGTM! Clean entrypoint and proper port configuration matching Cotejo proxy.", nil, false)

	_, _ = database.UpsertCheckRun(ctx, db.CheckRun{
		CLID:           1,
		PatchsetNumber: 2,
		RunnerName:     "package-builder",
		Provider:       "agent",
		Status:         "passed",
		ExternalURL:    "/cl/1/runs/package-builder/logs",
	})
	_, _ = database.UpsertCheckRun(ctx, db.CheckRun{
		CLID:           1,
		PatchsetNumber: 2,
		RunnerName:     "sec-bot",
		Provider:       "agent",
		Status:         "passed",
		ExternalURL:    "/cl/1/runs/sec-bot/logs",
	})

	fmt.Println("Successfully updated CL 1 with rich diffs, patchsets, reviewers, comments, and check runs!")
}
