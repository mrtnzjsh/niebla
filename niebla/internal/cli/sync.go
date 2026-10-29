package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"time"

	"niebla/internal/cli/config"
	nieblav1 "niebla/pkg/proto/nieblav1"

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
}

// NewSyncCmd constructs the 'sync' command for reconciling local workspaces with upstream trees.
func NewSyncCmd(rootFlags *RootFlags) *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize workspace with latest parent CL or trunk head",
		Long:  "Fetches the updated parent Merkle tree, reconciles local scratch changes, and reports or injects rebase conflicts.",
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
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			conn, client, err := DialGRPC(ctx, serverAddr)
			if err != nil {
				return err
			}
			defer conn.Close()

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
					fmt.Fprintln(cmd.ErrOrStderr(), "   Please resolve the conflicting files in your workspace, then run 'niebla shelve' to commit the resolution.")
				}
				return fmt.Errorf("rebase conflicts detected with parent CL %d", resp.ParentCl)
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

			ws.RootTreeHash = newTreeHex
			if err := mgr.Save(ws); err != nil {
				return fmt.Errorf("failed to save updated workspace metadata: %w", err)
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
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ Successfully synced workspace %q with CL %d (Patchset %d).\n", ws.Name, resp.ClId, resp.PatchsetNumber)
			fmt.Fprintf(cmd.OutOrStdout(), "  Base tree updated: %s -> %s\n", oldTreeHex, newTreeHex)
			return nil
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview sync operations without applying changes")

	return cmd
}
