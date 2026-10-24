package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"citc-toolchain/internal/cli/config"
	citcv1 "citc-toolchain/proto/v1"

	"github.com/spf13/cobra"
)

// NewSubmitCmd constructs the 'submit' command.
func NewSubmitCmd(rootFlags *RootFlags) *cobra.Command {
	var targetCL int64

	cmd := &cobra.Command{
		Use:   "submit",
		Short: "Submit a shelved changelist to trunk",
		Long:  "Atomically commits a shelved changelist to trunk head using the server backend.",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := config.NewManager(rootFlags.ConfigDir)
			if err != nil {
				return err
			}

			var activeWS *config.WorkspaceConfig
			clIDToSubmit := targetCL

			// If no explicit CL provided, resolve from active workspace
			if clIDToSubmit <= 0 {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("failed to get current working directory: %w", err)
				}

				ws, err := mgr.ResolveWorkspace(rootFlags.Workspace, cwd)
				if err != nil {
					return fmt.Errorf("no CL ID specified and %w; use --cl <id> or run inside an active workspace", err)
				}
				activeWS = ws

				if ws.ActiveCL <= 0 {
					return fmt.Errorf("workspace %q has no active shelved CL; specify --cl <id> or run 'citc shelve' first", ws.Name)
				}
				clIDToSubmit = ws.ActiveCL
			}

			serverAddr := ResolveServerAddr(rootFlags.Server)
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			conn, client, err := DialGRPC(ctx, serverAddr)
			if err != nil {
				return err
			}
			defer conn.Close()

			resp, err := client.SubmitCL(ctx, &citcv1.SubmitCLRequest{
				ClId: clIDToSubmit,
			})
			if err != nil {
				return fmt.Errorf("failed to submit CL %d: %w", clIDToSubmit, err)
			}

			// If submitted for the active workspace, update workspace metadata
			if activeWS != nil && activeWS.ActiveCL == clIDToSubmit {
				activeWS.ActiveCL = 0
				activeWS.BaseCL = resp.ClId
				if err := mgr.Save(activeWS); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to update workspace base CL: %v\n", err)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Successfully submitted CL %d at revision %d.\n", resp.ClId, resp.ClId)
			return nil
		},
	}

	cmd.Flags().Int64Var(&targetCL, "cl", 0, "Changelist ID to submit (defaults to active shelved CL in current workspace)")

	return cmd
}
