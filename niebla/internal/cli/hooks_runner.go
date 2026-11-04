package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"niebla/pkg/hooks"
)

// loadClientHooksConfig loads and parses .niebla/hooks.toml from the given mount directory.
// Returns nil, nil if the file does not exist.
func loadClientHooksConfig(mountDir string) (*hooks.Config, error) {
	hooksFile := filepath.Join(mountDir, ".niebla", "hooks.toml")
	hooksBytes, err := os.ReadFile(hooksFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", hooksFile, err)
	}
	return hooks.ParseTOML(hooksBytes)
}

// executeClientHook runs a single client command within the workspace mount directory.
func executeClientHook(
	ctx context.Context,
	mountDir string,
	hookType string,
	hook hooks.ClientHook,
	extraEnv []string,
	defaultFatalErr bool,
) error {
	if strings.TrimSpace(hook.Command) == "" {
		return nil
	}

	hookCmd := exec.CommandContext(ctx, "sh", "-c", hook.Command)
	hookCmd.Dir = mountDir
	hookCmd.Env = append(os.Environ(), extraEnv...)

	out, hErr := hookCmd.CombinedOutput()
	if hook.FailOnOutput && len(bytes.TrimSpace(out)) > 0 {
		return fmt.Errorf("%s %q failed (produced unexpected output, fail_on_output=true):\n%s", hookType, hook.Name, string(out))
	}

	if hErr != nil {
		isFatal := defaultFatalErr || hook.FailOnError
		if isFatal {
			return fmt.Errorf("%s %q failed with error: %w\n%s", hookType, hook.Name, hErr, string(out))
		}
		fmt.Fprintf(os.Stderr, "Warning: %s %q exited with error (fail_on_error=false): %v\n%s\n", hookType, hook.Name, hErr, string(out))
	}

	return nil
}

// runClientHooks executes a sequence of client hooks with the supplied environment.
func runClientHooks(
	ctx context.Context,
	mountDir string,
	hookType string,
	hookList []hooks.ClientHook,
	extraEnv []string,
	defaultFatalErr bool,
) error {
	for _, hook := range hookList {
		if err := executeClientHook(ctx, mountDir, hookType, hook, extraEnv, defaultFatalErr); err != nil {
			return err
		}
	}
	return nil
}

// runClientPresubmitHooks executes [client.presubmit] hooks before CL submission.
func runClientPresubmitHooks(ctx context.Context, mountDir string) error {
	cfg, err := loadClientHooksConfig(mountDir)
	if err != nil || cfg == nil || len(cfg.Client.Presubmit) == 0 {
		return err
	}
	return runClientHooks(ctx, mountDir, "client presubmit hook", cfg.Client.Presubmit, nil, true)
}

// runPostCreateHooks executes [client.post_create] hooks immediately after workspace mount.
func runPostCreateHooks(
	ctx context.Context,
	mountDir, workspaceName string,
	baseCL int64,
	serverAddr string,
) error {
	cfg, err := loadClientHooksConfig(mountDir)
	if err != nil || cfg == nil || len(cfg.Client.PostCreate) == 0 {
		return err
	}

	env := []string{
		fmt.Sprintf("NIEBLA_WORKSPACE_NAME=%s", workspaceName),
		fmt.Sprintf("NIEBLA_MOUNT_DIR=%s", mountDir),
		fmt.Sprintf("NIEBLA_BASE_CL=%d", baseCL),
		fmt.Sprintf("NIEBLA_SERVER=%s", serverAddr),
	}

	return runClientHooks(ctx, mountDir, "client post_create hook", cfg.Client.PostCreate, env, false)
}

// runPostSyncHooks executes [client.post_sync] hooks after a workspace sync operation.
func runPostSyncHooks(
	ctx context.Context,
	mountDir, workspaceName string,
	clID int64,
	serverAddr string,
) error {
	cfg, err := loadClientHooksConfig(mountDir)
	if err != nil || cfg == nil || len(cfg.Client.PostSync) == 0 {
		return err
	}

	env := []string{
		fmt.Sprintf("NIEBLA_WORKSPACE_NAME=%s", workspaceName),
		fmt.Sprintf("NIEBLA_MOUNT_DIR=%s", mountDir),
		fmt.Sprintf("NIEBLA_CL_ID=%d", clID),
		fmt.Sprintf("NIEBLA_SERVER=%s", serverAddr),
	}

	return runClientHooks(ctx, mountDir, "client post_sync hook", cfg.Client.PostSync, env, false)
}
