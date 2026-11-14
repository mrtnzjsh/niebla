package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"niebla/internal/cli/config"
	"niebla/pkg/materialize"
	"niebla/pkg/nieblafs/control"
	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"

	"github.com/spf13/cobra"
)

// SyncOutput represents the machine-readable JSON output for 'niebla sync --json'.
type SyncOutput struct {
	Workspace       string `json:"workspace"`
	CLID            int64  `json:"cl_id"`
	ParentCL        int64  `json:"parent_cl,omitempty"`
	PatchsetNumber  int32  `json:"patchset_number"`
	OldRootTreeHash string `json:"old_root_tree_hash"`
	NewRootTreeHash string `json:"new_root_tree_hash"`
	UpToDate        bool   `json:"up_to_date"`
	NeedsRebase     bool   `json:"needs_rebase"`
	ConflictDetails string `json:"conflict_details,omitempty"`
	DryRun          bool   `json:"dry_run"`
	ControlPurged   int    `json:"control_purged,omitempty"`
}

func handleSyncRebaseConflict(
	ctx context.Context,
	cmd *cobra.Command,
	ws *config.WorkspaceConfig,
	resp *nieblav1.SyncWorkspaceResponse,
	client nieblav1.NieblaServiceClient,
	oldTreeHex, newTreeHex string,
	rootFlags *RootFlags,
	dryRun bool,
) error {
	var conflictedPaths []string

	if !dryRun && len(resp.ParentRootTreeHash) > 0 {
		oldTreeBytes, _ := hex.DecodeString(oldTreeHex)
		fetchTree := makeTreeFetcher(ctx, client)
		readBlob := func(sha []byte) ([]byte, error) {
			var buf bytes.Buffer
			fb := makeBlobFetcher(client, append([]string{filepath.Join(filepath.Dir(ws.ScratchDir), "cache")}, defaultBlobCacheDirs()...))
			if err := fb(ctx, sha, &buf); err != nil {
				return nil, err
			}
			return buf.Bytes(), nil
		}
		writeBlob := func(data []byte) ([]byte, error) {
			sha := sha256.Sum256(data)
			return sha[:], nil
		}
		writeTree := func(entries []tree.Entry) ([]byte, error) {
			return tree.ComputeTreeHash(entries), nil
		}

		theirsLabel := fmt.Sprintf("PARENT CL %d", resp.ParentCl)
		mineLabel := fmt.Sprintf("LOCAL WORKSPACE (%s)", ws.Name)

		_, conflicts, _ := tree.ThreeWayTreeMerge(
			ctx,
			oldTreeBytes,
			resp.ParentRootTreeHash,
			resp.RootTreeHash,
			fetchTree,
			readBlob,
			writeBlob,
			writeTree,
			theirsLabel,
			mineLabel,
		)

		targetDir := ws.ScratchDir
		if ws.NoFuse || targetDir == "" {
			targetDir = ws.MountDir
		}

		for _, conflict := range conflicts {
			if len(conflict.ConflictedOutput) > 0 {
				conflictedPaths = append(conflictedPaths, conflict.Path)
				outPath := filepath.Join(targetDir, filepath.FromSlash(conflict.Path))
				_ = os.MkdirAll(filepath.Dir(outPath), 0755)
				_ = os.WriteFile(outPath, conflict.ConflictedOutput, 0644)
			}
		}

		// Reanchor FUSE daemon so it detects newly written scratch files
		if !ws.NoFuse {
			sockPath := ws.SocketPath
			if sockPath == "" && ws.ScratchDir != "" {
				sockPath = filepath.Join(filepath.Dir(ws.ScratchDir), "nieblafs.sock")
			}
			if sockPath != "" {
				if _, err := os.Stat(sockPath); err == nil {
					cClient := control.NewClient(sockPath)
					_, _ = cClient.Reanchor(ctx, oldTreeHex, resp.ClId)
				}
			}
		}
	}

	if rootFlags.JSON {
		_ = writeJSON(cmd.OutOrStdout(), SyncOutput{
			Workspace:       ws.Name,
			CLID:            resp.ClId,
			ParentCL:        resp.ParentCl,
			PatchsetNumber:  resp.PatchsetNumber,
			OldRootTreeHash: oldTreeHex,
			NewRootTreeHash: newTreeHex,
			UpToDate:        false,
			NeedsRebase:     true,
			ConflictDetails: resp.RebaseConflictDetails,
			DryRun:          dryRun,
		})
	} else {
		fmt.Fprintf(cmd.ErrOrStderr(), "⚠️  Rebase conflict detected with Parent CL %d!\n", resp.ParentCl)
		if resp.RebaseConflictDetails != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "   Conflicts: %s\n", resp.RebaseConflictDetails)
		}
		if len(conflictedPaths) > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "   ✓ Injected conflict markers into %d workspace file(s):\n", len(conflictedPaths))
			for _, cp := range conflictedPaths {
				fmt.Fprintf(cmd.ErrOrStderr(), "     • %s\n", cp)
			}
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "   Please resolve the conflicting files in your workspace, then run 'niebla shelve' to commit the resolution.")
	}
	return fmt.Errorf("rebase conflicts detected with parent CL %d: please resolve in editor and shelve", resp.ParentCl)
}

func applyWorkspaceSyncUpdate(
	ctx context.Context,
	cmd *cobra.Command,
	ws *config.WorkspaceConfig,
	resp *nieblav1.SyncWorkspaceResponse,
	client nieblav1.NieblaServiceClient,
	newTreeHex string,
) (int, error) {
	var controlPurged int
	if ws.NoFuse {
		fetchTree := makeTreeFetcher(ctx, client)
		fetchBlob := makeBlobFetcher(client, append([]string{filepath.Join(filepath.Dir(ws.ScratchDir), "cache")}, defaultBlobCacheDirs()...))
		oldTreeBytes, _ := hex.DecodeString(ws.RootTreeHash)
		matOpts := materialize.Options{
			PruneStaleFiles: true,
			OldTreeHash:     oldTreeBytes,
		}
		stats, err := materialize.MaterializeTree(ctx, resp.RootTreeHash, ws.MountDir, fetchTree, fetchBlob, matOpts)
		if err != nil {
			return 0, fmt.Errorf("failed to materialize updated tree into %s: %w", ws.MountDir, err)
		}
		if stats != nil && stats.FilesPruned > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  Pruned %d stale upstream deleted file(s).\n", stats.FilesPruned)
		}
		return 0, nil
	}

	sockPath := ws.SocketPath
	if sockPath == "" && ws.ScratchDir != "" {
		sockPath = filepath.Join(filepath.Dir(ws.ScratchDir), "nieblafs.sock")
	}
	if sockPath != "" {
		if _, err := os.Stat(sockPath); err == nil {
			cClient := control.NewClient(sockPath)
			cResp, err := cClient.Reanchor(ctx, newTreeHex, resp.ClId)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to re-anchor nieblafs daemon via control socket: %v\n", err)
			} else {
				controlPurged = cResp.PurgedEntries
			}
		}
	}
	return controlPurged, nil
}

func executeWorkspaceSync(cmd *cobra.Command, rootFlags *RootFlags, dryRun bool, noHooks bool, force bool) error {
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
	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()

	conn, client, err := DialGRPC(ctx, serverAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	if ws.NoFuse && !force && !dryRun {
		oldTreeBytes, _ := hex.DecodeString(ws.RootTreeHash)
		fetchTree := makeTreeFetcher(ctx, client)
		changes, err := ScanWorkspaceChanges(ws, oldTreeBytes, fetchTree)
		if err == nil && len(changes) > 0 {
			return fmt.Errorf("workspace %q has %d uncommitted modification(s). Run 'niebla shelve' to save your work, or 'niebla sync --force' to overwrite", ws.Name, len(changes))
		}
	}

	targetCL := ws.ActiveCL
	if targetCL <= 0 {
		targetCL = ws.BaseCL
	}

	resp, err := client.SyncWorkspace(ctx, &nieblav1.SyncWorkspaceRequest{
		WorkspaceId: ws.WorkspaceID,
		ClId:        targetCL,
	})
	if err != nil {
		return fmt.Errorf("failed to sync workspace with server: %w", err)
	}

	newTreeHex := hex.EncodeToString(resp.RootTreeHash)
	oldTreeHex := ws.RootTreeHash

	if resp.NeedsRebase {
		return handleSyncRebaseConflict(ctx, cmd, ws, resp, client, oldTreeHex, newTreeHex, rootFlags, dryRun)
	}

	if oldTreeHex == newTreeHex {
		if rootFlags.JSON {
			return writeJSON(cmd.OutOrStdout(), SyncOutput{
				Workspace:       ws.Name,
				CLID:            resp.ClId,
				ParentCL:        resp.ParentCl,
				PatchsetNumber:  resp.PatchsetNumber,
				OldRootTreeHash: oldTreeHex,
				NewRootTreeHash: newTreeHex,
				UpToDate:        true,
				NeedsRebase:     false,
				DryRun:          dryRun,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q is already up to date with CL %d (Patchset %d).\n", ws.Name, resp.ClId, resp.PatchsetNumber)
		return nil
	}

	if dryRun {
		if rootFlags.JSON {
			return writeJSON(cmd.OutOrStdout(), SyncOutput{
				Workspace:       ws.Name,
				CLID:            resp.ClId,
				ParentCL:        resp.ParentCl,
				PatchsetNumber:  resp.PatchsetNumber,
				OldRootTreeHash: oldTreeHex,
				NewRootTreeHash: newTreeHex,
				UpToDate:        false,
				NeedsRebase:     false,
				DryRun:          true,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "[Dry Run] Workspace %q can be updated: %s -> %s (Patchset %d)\n", ws.Name, ws.RootTreeHash, newTreeHex, resp.PatchsetNumber)
		return nil
	}

	controlPurged, err := applyWorkspaceSyncUpdate(ctx, cmd, ws, resp, client, newTreeHex)
	if err != nil {
		return err
	}

	ws.RootTreeHash = newTreeHex
	if err := mgr.Save(ws); err != nil {
		return fmt.Errorf("failed to save updated workspace metadata: %w", err)
	}

	if !noHooks {
		if err := runPostSyncHooks(ctx, ws.MountDir, ws.Name, resp.ClId, serverAddr); err != nil {
			return fmt.Errorf("post_sync hooks failed: %w", err)
		}
	}

	if rootFlags.JSON {
		return writeJSON(cmd.OutOrStdout(), SyncOutput{
			Workspace:       ws.Name,
			CLID:            resp.ClId,
			ParentCL:        resp.ParentCl,
			PatchsetNumber:  resp.PatchsetNumber,
			OldRootTreeHash: oldTreeHex,
			NewRootTreeHash: newTreeHex,
			UpToDate:        true,
			NeedsRebase:     false,
			DryRun:          false,
			ControlPurged:   controlPurged,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Successfully synced workspace %q with CL %d (Patchset %d).\n", ws.Name, resp.ClId, resp.PatchsetNumber)
	fmt.Fprintf(cmd.OutOrStdout(), "  Base tree updated: %s -> %s\n", oldTreeHex, newTreeHex)
	if controlPurged > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Virtual filesystem re-anchored (%d cached inodes invalidated).\n", controlPurged)
	} else if ws.NoFuse {
		fmt.Fprintln(cmd.OutOrStdout(), "  Direct filesystem materialization updated.")
	}
	return nil
}

// NewSyncCmd constructs the 'sync' command for reconciling local workspaces with upstream trees.
func NewSyncCmd(rootFlags *RootFlags) *cobra.Command {
	var dryRun bool
	var noHooks bool
	var force bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize workspace with latest parent CL or trunk head",
		Long:  "Fetches the updated parent Merkle tree, reconciles local scratch changes, and reports or injects rebase conflicts.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return executeWorkspaceSync(cmd, rootFlags, dryRun, noHooks, force)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview sync operations without applying changes")
	cmd.Flags().BoolVar(&noHooks, "no-hooks", false, "Skip client post_sync lifecycle hooks")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force synchronization even if local modifications exist in direct materialization mode")

	return cmd
}
