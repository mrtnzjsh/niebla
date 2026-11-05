package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"niebla/internal/cli/config"
	"niebla/pkg/ignore"
	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// ChangeType represents the classification of a working copy file.
type ChangeType string

const (
	ChangeModified ChangeType = "M"
	ChangeAdded    ChangeType = "A"
	ChangeDeleted  ChangeType = "D"
)

// FileChange describes a single modified, added, or deleted file.
type FileChange struct {
	Path      string      `json:"path"`
	Type      ChangeType  `json:"type"`
	DiskPath  string      `json:"-"`
	BaseEntry *tree.Entry `json:"-"`
}

// StatusOutput represents the machine-readable JSON output for 'niebla status --json'.
type StatusOutput struct {
	Workspace    string       `json:"workspace"`
	BaseCL       int64        `json:"base_cl"`
	ActiveCL     int64        `json:"active_cl,omitempty"`
	RootTreeHash string       `json:"root_tree_hash"`
	Clean        bool         `json:"clean"`
	Changes      []FileChange `json:"changes"`
}

// NewStatusCmd constructs the 'status' command.
func NewStatusCmd(rootFlags *RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show workspace changes in the scratch overlay",
		Long:  "Inspects modified, added, and deleted files in the local workspace scratch overlay against the base tree.",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := config.NewManager(rootFlags.ConfigDir)
			if err != nil {
				return err
			}

			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current working directory: %w", err)
			}

			ws, err := mgr.ResolveWorkspace(rootFlags.Workspace, cwd)
			if err != nil {
				return err
			}

			serverAddr := ResolveServerAddr(rootFlags.Server)
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			conn, client, err := DialGRPC(ctx, serverAddr)
			if err != nil {
				return err
			}
			defer conn.Close()

			rootTreeHash, err := hex.DecodeString(ws.RootTreeHash)
			if err != nil {
				return fmt.Errorf("invalid root tree hash %q in workspace config: %w", ws.RootTreeHash, err)
			}

			fetchTree := makeTreeFetcher(ctx, client)
			changes, err := ScanWorkspaceChanges(ws, rootTreeHash, fetchTree)
			if err != nil {
				return fmt.Errorf("failed to scan workspace changes: %w", err)
			}

			if rootFlags.JSON {
				if changes == nil {
					changes = []FileChange{}
				}
				return writeJSON(cmd.OutOrStdout(), StatusOutput{
					Workspace:    ws.Name,
					BaseCL:       ws.BaseCL,
					ActiveCL:     ws.ActiveCL,
					RootTreeHash: ws.RootTreeHash,
					Clean:        len(changes) == 0,
					Changes:      changes,
				})
			}

			renderStatus(cmd.OutOrStdout(), changes)
			return nil
		},
	}

	return cmd
}

// loadWorkspaceMatcher loads .nieblaignore from ScratchDir or MountDir if present.
func loadWorkspaceMatcher(ws *config.WorkspaceConfig) ignore.Matcher {
	if ws != nil {
		for _, dir := range []string{ws.ScratchDir, ws.MountDir} {
			if dir == "" {
				continue
			}
			ignorePath := filepath.Join(dir, ".nieblaignore")
			if _, err := os.Stat(ignorePath); err == nil {
				if m, err := ignore.LoadFile(ignorePath); err == nil {
					return m
				}
			}
		}
	}
	return ignore.NewMatcher()
}

// ScanWorkspaceChanges scans workspace changes, including missing base files for --no-fuse workspaces.
func ScanWorkspaceChanges(ws *config.WorkspaceConfig, rootTreeHash []byte, fetchTree tree.FetchTreeFunc) ([]FileChange, error) {
	matcher := loadWorkspaceMatcher(ws)
	changes, err := ScanScratchChanges(ws.ScratchDir, rootTreeHash, fetchTree, matcher)
	if err != nil {
		return nil, err
	}
	if ws == nil || !ws.NoFuse || len(rootTreeHash) == 0 || bytes.Equal(rootTreeHash, tree.EmptyTreeHash) {
		return changes, nil
	}

	seen := make(map[string]bool, len(changes))
	for _, change := range changes {
		seen[change.Path] = true
	}

	var walkBase func(treeHash []byte, prefix string) error
	walkBase = func(treeHash []byte, prefix string) error {
		if len(treeHash) == 0 || bytes.Equal(treeHash, tree.EmptyTreeHash) {
			return nil
		}
		entries, err := fetchTree(treeHash)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			relPath := entry.Name
			if prefix != "" {
				relPath = prefix + "/" + entry.Name
			}
			if matcher != nil && matcher.Match(relPath, entry.IsDir()) {
				continue
			}
			if entry.IsDir() {
				if err := walkBase(entry.SubTreeHash, relPath); err != nil {
					return err
				}
			} else if !seen[relPath] {
				diskPath := filepath.Join(ws.ScratchDir, filepath.FromSlash(relPath))
				if _, statErr := os.Stat(diskPath); os.IsNotExist(statErr) {
					entryCopy := entry
					changes = append(changes, FileChange{
						Path:      relPath,
						Type:      ChangeDeleted,
						DiskPath:  diskPath,
						BaseEntry: &entryCopy,
					})
					seen[relPath] = true
				}
			}
		}
		return nil
	}

	if err := walkBase(rootTreeHash, ""); err != nil {
		return nil, err
	}

	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})
	return changes, nil
}

func detectWhiteoutOriginalPath(fileName, rel string, info fs.FileInfo) (string, bool) {
	isWhiteoutCharDev := (info.Mode() & os.ModeCharDevice) != 0
	isPrefixWhiteout := strings.HasPrefix(fileName, ".wh.")
	isTombstone := strings.HasSuffix(fileName, ".tombstone") || strings.HasSuffix(fileName, ".wh")

	if !isWhiteoutCharDev && !isPrefixWhiteout && !isTombstone {
		return "", false
	}

	dir := filepath.Dir(rel)
	if isPrefixWhiteout {
		originalName := strings.TrimPrefix(fileName, ".wh.")
		if dir == "." {
			return originalName, true
		}
		return filepath.ToSlash(filepath.Join(dir, originalName)), true
	}
	if isTombstone {
		originalName := strings.TrimSuffix(strings.TrimSuffix(fileName, ".tombstone"), ".wh")
		if dir == "." {
			return originalName, true
		}
		return filepath.ToSlash(filepath.Join(dir, originalName)), true
	}
	return rel, true
}

func classifyRegularScratchFile(
	diskPath, rel string,
	info fs.FileInfo,
	rootTreeHash []byte,
	fetchTree tree.FetchTreeFunc,
) *FileChange {
	baseEntry, resolveErr := tree.ResolvePath(rootTreeHash, rel, fetchTree)
	if resolveErr != nil || baseEntry == nil || baseEntry.IsDir() {
		return &FileChange{
			Path:      rel,
			Type:      ChangeAdded,
			DiskPath:  diskPath,
			BaseEntry: nil,
		}
	}

	if len(baseEntry.BlobSHA256) == 32 {
		if data, readErr := os.ReadFile(diskPath); readErr == nil {
			sum := sha256.Sum256(data)
			diskExec := (info.Mode() & 0111) != 0
			baseExec := (baseEntry.Mode & 0111) != 0
			if bytes.Equal(sum[:], baseEntry.BlobSHA256) && diskExec == baseExec {
				return nil
			}
		}
	}

	return &FileChange{
		Path:      rel,
		Type:      ChangeModified,
		DiskPath:  diskPath,
		BaseEntry: baseEntry,
	}
}

// ScanScratchChanges inspects scratchDir and compares against rootTreeHash to identify M, A, D files.
func ScanScratchChanges(scratchDir string, rootTreeHash []byte, fetchTree tree.FetchTreeFunc, matchers ...ignore.Matcher) ([]FileChange, error) {
	if _, err := os.Stat(scratchDir); os.IsNotExist(err) {
		return []FileChange{}, nil
	}

	var matcher ignore.Matcher
	if len(matchers) > 0 && matchers[0] != nil {
		matcher = matchers[0]
	} else {
		var err error
		matcher, err = ignore.LoadFile(filepath.Join(scratchDir, ".nieblaignore"))
		if err != nil {
			matcher = ignore.NewMatcher()
		}
	}

	changeMap := make(map[string]FileChange)

	err := filepath.WalkDir(scratchDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(scratchDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		// Normalize to forward slashes
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			if matcher.Match(rel, true) {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		// Detect deletion via Linux overlayfs whiteout character device or tombstone files
		if origPath, isWhiteout := detectWhiteoutOriginalPath(d.Name(), rel, info); isWhiteout {
			if matcher.Match(origPath, false) {
				return nil
			}
			baseEntry, _ := tree.ResolvePath(rootTreeHash, origPath, fetchTree)
			changeMap[origPath] = FileChange{
				Path:      origPath,
				Type:      ChangeDeleted,
				DiskPath:  path,
				BaseEntry: baseEntry,
			}
			return nil
		}

		if matcher.Match(rel, false) {
			return nil
		}

		if ch := classifyRegularScratchFile(path, rel, info, rootTreeHash, fetchTree); ch != nil {
			changeMap[rel] = *ch
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	changes := make([]FileChange, 0, len(changeMap))
	for _, change := range changeMap {
		changes = append(changes, change)
	}

	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})

	return changes, nil
}

// makeTreeFetcher creates a cached tree fetcher function over gRPC.
func makeTreeFetcher(ctx context.Context, client nieblav1.NieblaServiceClient) tree.FetchTreeFunc {
	cache := make(map[string][]tree.Entry)

	return func(treeHash []byte) ([]tree.Entry, error) {
		key := hex.EncodeToString(treeHash)
		if cached, ok := cache[key]; ok {
			return cached, nil
		}

		resp, err := client.GetTree(ctx, &nieblav1.GetTreeRequest{
			TreeHash: treeHash,
		})
		if err != nil {
			return nil, err
		}

		entries := make([]tree.Entry, len(resp.Entries))
		for i, pe := range resp.Entries {
			entries[i] = tree.FromProto(pe)
		}

		cache[key] = entries
		return entries, nil
	}
}

func renderStatus(out io.Writer, changes []FileChange) {
	useColor := isColorEnabled(out)

	for _, change := range changes {
		var indicator string
		switch change.Type {
		case ChangeModified:
			if useColor {
				indicator = "\033[33mM\033[0m" // Yellow
			} else {
				indicator = "M"
			}
		case ChangeAdded:
			if useColor {
				indicator = "\033[32mA\033[0m" // Green
			} else {
				indicator = "A"
			}
		case ChangeDeleted:
			if useColor {
				indicator = "\033[31mD\033[0m" // Red
			} else {
				indicator = "D"
			}
		}

		fmt.Fprintf(out, "%s  %s\n", indicator, change.Path)
	}
}

func isColorEnabled(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	if f, ok := w.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}
