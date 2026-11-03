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

func runClientPresubmitHooks(ctx context.Context, mountDir string) error {
	hooksFile := filepath.Join(mountDir, ".niebla", "hooks.toml")
	hooksBytes, err := os.ReadFile(hooksFile)
	if err != nil {
		return nil
	}
	hooksCfg, err := hooks.ParseTOML(hooksBytes)
	if err != nil || hooksCfg == nil || len(hooksCfg.Client.Presubmit) == 0 {
		return nil
	}

	for _, clientHook := range hooksCfg.Client.Presubmit {
		if clientHook.Command == "" {
			continue
		}
		hookCmd := exec.CommandContext(ctx, "sh", "-c", clientHook.Command)
		hookCmd.Dir = mountDir
		out, hErr := hookCmd.CombinedOutput()
		if clientHook.FailOnOutput && len(bytes.TrimSpace(out)) > 0 {
			return fmt.Errorf("client presubmit hook %q failed (produced unexpected output, fail_on_output=true):\n%s", clientHook.Name, string(out))
		}
		if hErr != nil {
			return fmt.Errorf("client presubmit hook %q failed with error: %w\n%s", clientHook.Name, hErr, string(out))
		}
	}
	return nil
}

func resolveSubmitCLID(mgr *config.Manager, rootFlags *RootFlags, targetCL int64) (int64, *config.WorkspaceConfig, error) {
	if targetCL > 0 {
		return targetCL, nil, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return 0, nil, fmt.Errorf("failed to get current working directory: %w", err)
	}

	ws, err := mgr.ResolveWorkspace(rootFlags.Workspace, cwd)
	if err != nil {
		return 0, nil, fmt.Errorf("no CL ID specified and %w; use --cl <id> or run inside an active workspace", err)
	}

	if ws.ActiveCL <= 0 {
		return 0, nil, fmt.Errorf("workspace %q has no active shelved CL; specify --cl <id> or run 'niebla shelve' first", ws.Name)
	}
	return ws.ActiveCL, ws, nil
}

func executeSubmit(cmd *cobra.Command, rootFlags *RootFlags, targetCL int64, force, noVerify bool) error {
	mgr, err := config.NewManager(rootFlags.ConfigDir)
	if err != nil {
		return err
	}

	clIDToSubmit, activeWS, err := resolveSubmitCLID(mgr, rootFlags, targetCL)
	if err != nil {
		return err
	}

	if activeWS != nil && !noVerify {
		if err := runClientPresubmitHooks(cmd.Context(), activeWS.MountDir); err != nil {
			return err
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
			return executeSubmit(cmd, rootFlags, targetCL, force, noVerify)
		},
	}

	cmd.Flags().Int64Var(&targetCL, "cl", 0, "Changelist ID to submit (defaults to active shelved CL in current workspace)")
	cmd.Flags().BoolVar(&force, "force", false, "Force submission, bypassing unresolved comments and approval requirements")
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "Bypass client-side presubmit hooks")

	return cmd
}
