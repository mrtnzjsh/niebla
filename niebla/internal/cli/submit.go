package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"niebla/internal/cli/config"
	"niebla/pkg/hooks"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"github.com/spf13/cobra"
)

// SubmitOutput represents the machine-readable JSON output for 'niebla submit --json'.
type SubmitOutput struct {
	CLID            int64  `json:"cl_id"`
	Status          string `json:"status"`
	CommittedAtUnix int64  `json:"committed_at_unix"`
}

// NewSubmitCmd constructs the 'submit' command.
func NewSubmitCmd(rootFlags *RootFlags) *cobra.Command {
	var targetCL int64
	var force bool
	var noVerify bool

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
					return fmt.Errorf("workspace %q has no active shelved CL; specify --cl <id> or run 'niebla shelve' first", ws.Name)
				}
				clIDToSubmit = ws.ActiveCL
			}

			// Run client-side presubmit hooks if present and not bypassed
			if activeWS != nil && !noVerify {
				hooksFile := filepath.Join(activeWS.MountDir, ".niebla", "hooks.toml")
				if hooksBytes, err := os.ReadFile(hooksFile); err == nil {
					if hooksCfg, err := hooks.ParseTOML(hooksBytes); err == nil && hooksCfg != nil && len(hooksCfg.Client.Presubmit) > 0 {
						for _, clientHook := range hooksCfg.Client.Presubmit {
							if clientHook.Command == "" {
								continue
							}
							hookCmd := exec.CommandContext(cmd.Context(), "sh", "-c", clientHook.Command)
							hookCmd.Dir = activeWS.MountDir
							out, hErr := hookCmd.CombinedOutput()
							if clientHook.FailOnOutput && len(bytes.TrimSpace(out)) > 0 {
								return fmt.Errorf("client presubmit hook %q failed (produced unexpected output, fail_on_output=true):\n%s", clientHook.Name, string(out))
							}
							if hErr != nil {
								return fmt.Errorf("client presubmit hook %q failed with error: %w\n%s", clientHook.Name, hErr, string(out))
							}
						}
					}
				}
			}

			serverAddr := ResolveServerAddr(rootFlags.Server)
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			conn, client, err := DialGRPC(ctx, serverAddr)
			if err != nil {
				return err
			}
			defer conn.Close()

			resp, err := client.SubmitCL(ctx, &nieblav1.SubmitCLRequest{
				ClId:  clIDToSubmit,
				Force: force,
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

			if rootFlags.JSON {
				return writeJSON(cmd.OutOrStdout(), SubmitOutput{
					CLID:            resp.ClId,
					Status:          "submitted",
					CommittedAtUnix: resp.CommittedAtUnix,
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Successfully submitted CL %d at revision %d.\n", resp.ClId, resp.ClId)
			return nil
		},
	}

	cmd.Flags().Int64Var(&targetCL, "cl", 0, "Changelist ID to submit (defaults to active shelved CL in current workspace)")
	cmd.Flags().BoolVar(&force, "force", false, "Force submission, bypassing unresolved comments and approval requirements")
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "Bypass client-side presubmit hooks")

	return cmd
}
