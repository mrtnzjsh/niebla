package cli

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"niebla/internal/cli/config"
	"niebla/internal/cli/skills"
	toolchaincfg "niebla/internal/config"
	"niebla/pkg/materialize"
	"niebla/pkg/nieblafs/control"
	"niebla/pkg/nieblafs/preflight"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"github.com/spf13/cobra"
)

// NewWorkspaceCmd creates the 'workspace' parent command and its subcommands.
func NewWorkspaceCmd(rootFlags *RootFlags) *cobra.Command {
	wsCmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage Niebla virtual workspaces",
		Long:  "Commands to create, list, mount, unmount, and delete virtual workspaces backed by Niebla server and FUSE.",
	}

	wsCmd.AddCommand(newWorkspaceCreateCmd(rootFlags))
	wsCmd.AddCommand(newWorkspaceListCmd(rootFlags))
	wsCmd.AddCommand(newWorkspaceDeleteCmd(rootFlags))
	wsCmd.AddCommand(newWorkspaceMountCmd(rootFlags))
	wsCmd.AddCommand(newWorkspaceUnmountCmd(rootFlags))

	return wsCmd
}

func resolveWorkspaceMountDir(mountDir, name string) (string, error) {
	resolved := mountDir
	if resolved == "" {
		mountBase := ""
		if tcfg, _ := toolchaincfg.Load(); tcfg != nil && tcfg.FS.MountBase != "" {
			mountBase = os.ExpandEnv(tcfg.FS.MountBase)
		}
		if mountBase == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get user home directory: %w", err)
			}
			mountBase = filepath.Join(home, "niebla", "workspaces")
		}

		if err := os.MkdirAll(mountBase, 0755); err == nil {
			resolved = filepath.Join(mountBase, name)
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to get user home directory: %w", err)
			}
			resolved = filepath.Join(home, "niebla", name)
		}
	}
	var err error
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("failed to resolve absolute mount path: %w", err)
	}

	return resolved, nil
}

// WorkspaceFSInfo contains connection and process details for a mounted workspace.
type WorkspaceFSInfo struct {
	DaemonPID  int
	SocketPath string
	ScratchDir string
}

func setupWorkspaceFS(
	ctx context.Context,
	cmd *cobra.Command,
	noFuse bool,
	name, resolvedMountDir, scratchDir, cacheDir, rootTreeHashHex, serverAddr string,
	client nieblav1.NieblaServiceClient,
	rootTreeHash []byte,
) (*WorkspaceFSInfo, error) {
	if noFuse {
		fetchTree := makeTreeFetcher(ctx, client)
		fetchBlob := makeBlobFetcher(client, append([]string{cacheDir}, defaultBlobCacheDirs()...))
		if _, err := materialize.MaterializeTree(ctx, rootTreeHash, resolvedMountDir, fetchTree, fetchBlob, materialize.Options{}); err != nil {
			return nil, fmt.Errorf("failed to materialize workspace %q in --no-fuse mode: %w", name, err)
		}
		return &WorkspaceFSInfo{
			DaemonPID:  0,
			SocketPath: "",
			ScratchDir: resolvedMountDir,
		}, nil
	}

	checkFUSEEnvironment(cmd.ErrOrStderr())
	socketPath := filepath.Join(filepath.Dir(scratchDir), "nieblafs.sock")
	var daemonPID int
	nieblafsBin, err := findNieblafsBinary()
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: nieblafs binary not found in PATH or next to niebla; FUSE daemon not started.\n")
	} else {
		pid, err := spawnNieblafs(nieblafsBin, resolvedMountDir, scratchDir, cacheDir, rootTreeHashHex, serverAddr, socketPath)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to spawn nieblafs daemon: %v\n", err)
		} else {
			daemonPID = pid
		}
	}
	return &WorkspaceFSInfo{
		DaemonPID:  daemonPID,
		SocketPath: socketPath,
		ScratchDir: scratchDir,
	}, nil
}

func executeWorkspaceCreate(
	cmd *cobra.Command,
	rootFlags *RootFlags,
	name string,
	baseCL int64,
	mountDir string,
	noFuse bool,
	noHooks bool,
) error {
	serverAddr := ResolveServerAddr(rootFlags.Server)

	mgr, err := config.NewManager(rootFlags.ConfigDir)
	if err != nil {
		return err
	}

	if _, err := mgr.Load(name); err == nil {
		return fmt.Errorf("workspace %q already exists locally in %s", name, mgr.ConfigFilePath(name))
	}

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	conn, client, err := DialGRPC(ctx, serverAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	owner := getCurrentUsername()
	resp, err := client.CreateWorkspace(ctx, &nieblav1.CreateWorkspaceRequest{
		Name:   name,
		Owner:  owner,
		BaseCl: baseCL,
	})
	if err != nil {
		return fmt.Errorf("server failed to create workspace %q: %w", name, err)
	}

	rootTreeHashHex := hex.EncodeToString(resp.RootTreeHash)

	scratchDir, cacheDir, err := provisionWorkspaceDirs(name)
	if err != nil {
		return fmt.Errorf("failed to provision workspace directories: %w", err)
	}

	resolvedMountDir, err := resolveWorkspaceMountDir(mountDir, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(resolvedMountDir, 0755); err != nil {
		return fmt.Errorf("failed to create mount directory %s: %w", resolvedMountDir, err)
	}

	fsInfo, err := setupWorkspaceFS(
		ctx, cmd, noFuse, name, resolvedMountDir, scratchDir, cacheDir, rootTreeHashHex, serverAddr, client, resp.RootTreeHash,
	)
	if err != nil {
		return err
	}

	wsConfig := &config.WorkspaceConfig{
		WorkspaceID:  resp.WorkspaceId,
		Name:         name,
		BaseCL:       resp.BaseCl,
		RootTreeHash: rootTreeHashHex,
		MountDir:     resolvedMountDir,
		ScratchDir:   fsInfo.ScratchDir,
		PID:          fsInfo.DaemonPID,
		NoFuse:       noFuse,
		SocketPath:   fsInfo.SocketPath,
	}

	if err := mgr.Save(wsConfig); err != nil {
		return fmt.Errorf("failed to write workspace metadata: %w", err)
	}

	if fsInfo.DaemonPID > 0 {
		pollMountActive(cmd.OutOrStdout(), fsInfo.DaemonPID, resolvedMountDir, fsInfo.SocketPath, fsInfo.ScratchDir)
	}

	go func() {
		if hubDir, err := skills.EnsureHub(""); err == nil {
			_, _ = skills.SyncSpokes(hubDir, false, "")
		}
	}()

	if !noHooks {
		if err := runPostCreateHooks(ctx, resolvedMountDir, name, resp.BaseCl, serverAddr); err != nil {
			return fmt.Errorf("post_create hooks failed: %w", err)
		}
	}

	if rootFlags.JSON {
		return writeJSON(cmd.OutOrStdout(), wsConfig)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q created successfully.\n", name)
	fmt.Fprintf(cmd.OutOrStdout(), "  Base CL   : %d\n", resp.BaseCl)
	fmt.Fprintf(cmd.OutOrStdout(), "  Tree Hash : %s\n", rootTreeHashHex)
	fmt.Fprintf(cmd.OutOrStdout(), "  Mount Dir : %s\n", resolvedMountDir)
	fmt.Fprintf(cmd.OutOrStdout(), "  Scratch   : %s\n", fsInfo.ScratchDir)
	if noFuse {
		fmt.Fprintf(cmd.OutOrStdout(), "  Mode      : NO-FUSE (direct materialization)\n")
	} else if fsInfo.DaemonPID > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Daemon PID: %d\n", fsInfo.DaemonPID)
	}

	return nil
}

func newWorkspaceCreateCmd(rootFlags *RootFlags) *cobra.Command {
	var baseCL int64
	var mountDir string
	var noFuse bool
	var noHooks bool

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create and mount a new Niebla virtual workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return executeWorkspaceCreate(cmd, rootFlags, args[0], baseCL, mountDir, noFuse, noHooks)
		},
	}

	cmd.Flags().Int64Var(&baseCL, "cl", 0, "Base Changelist ID to branch workspace from (0 = trunk head)")
	cmd.Flags().Int64Var(&baseCL, "base-cl", 0, "Alias for --cl")
	cmd.Flags().StringVar(&mountDir, "mount-dir", "", "Custom mount directory (defaults to /niebla/workspaces/<name>)")
	cmd.Flags().BoolVar(&noFuse, "no-fuse", false, "Materialize workspace directly to disk without mounting FUSE")
	cmd.Flags().BoolVar(&noHooks, "no-hooks", false, "Skip client post_create lifecycle hooks")

	return cmd
}

type workspaceListEntry struct {
	*config.WorkspaceConfig
	Status string `json:"status"`
}

func newWorkspaceListCmd(rootFlags *RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all Niebla workspaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := config.NewManager(rootFlags.ConfigDir)
			if err != nil {
				return err
			}

			workspaces, err := mgr.List()
			if err != nil {
				return err
			}

			if rootFlags.JSON {
				entries := make([]workspaceListEntry, 0, len(workspaces))
				for _, ws := range workspaces {
					entries = append(entries, workspaceListEntry{
						WorkspaceConfig: ws,
						Status:          checkWorkspaceStatus(ws),
					})
				}
				return writeJSON(cmd.OutOrStdout(), entries)
			}

			if len(workspaces) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No Niebla workspaces found.")
				return nil
			}

			writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 4, ' ', 0)
			fmt.Fprintln(writer, "NAME\tBASE CL\tMOUNT PATH\tSTATUS")

			for _, ws := range workspaces {
				status := checkWorkspaceStatus(ws)
				fmt.Fprintf(writer, "%s\t%d\t%s\t%s\n", ws.Name, ws.BaseCL, ws.MountDir, status)
			}

			return writer.Flush()
		},
	}
}

func newWorkspaceDeleteCmd(rootFlags *RootFlags) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"destroy", "rm"},
		Short:   "Delete a virtual workspace and unmount it",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			mgr, err := config.NewManager(rootFlags.ConfigDir)
			if err != nil {
				return err
			}

			ws, err := mgr.Load(name)
			if err != nil {
				if !force {
					return err
				}
				// When force is true, metadata might be missing or out of sync.
				// Infer default workspace paths to clean up orphaned mounts and directories.
				mountDir, _ := resolveWorkspaceMountDir("", name)
				scratchDir, _, _ := provisionWorkspaceDirs(name)
				socketPath := filepath.Join(filepath.Dir(scratchDir), "nieblafs.sock")
				ws = &config.WorkspaceConfig{
					Name:       name,
					MountDir:   mountDir,
					ScratchDir: scratchDir,
					SocketPath: socketPath,
				}
			}

			// Check for modifications if not forced
			if !force {
				hasDirty, err := hasScratchModifications(ws.ScratchDir)
				if err == nil && hasDirty {
					return fmt.Errorf("workspace %q has unshelved modifications in %s; use --force to delete anyway", name, ws.ScratchDir)
				}
			}

			// If mount directory is not set, attempt resolution
			if ws.MountDir == "" {
				ws.MountDir, _ = resolveWorkspaceMountDir("", name)
			}

			// Terminate daemon process if running
			if ws.PID > 0 && isProcessAlive(ws.PID) {
				terminateProcess(ws.PID)
			}

			// Detach and unmount if mountpoint
			if isMountPoint(ws.MountDir) || force {
				if err := unmountWorkspace(ws.MountDir, force); err != nil {
					if !force {
						fmt.Fprintf(cmd.ErrOrStderr(), "Warning: unmounting %s failed: %v\n", ws.MountDir, err)
					}
				}
			}

			if ws.SocketPath != "" {
				_ = os.Remove(ws.SocketPath)
			}
			if ws.ScratchDir != "" {
				_ = os.RemoveAll(ws.ScratchDir)
				parent := filepath.Dir(ws.ScratchDir)
				_ = os.Remove(parent)
			}
			if ws.MountDir != "" {
				_ = os.RemoveAll(ws.MountDir)
			}

			if err := mgr.Delete(name); err != nil {
				if !force {
					return fmt.Errorf("failed to remove workspace metadata: %w", err)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q deleted successfully.\n", name)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force deletion even with unshelved modifications or missing metadata")

	return cmd
}

func newWorkspaceMountCmd(rootFlags *RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mount <name>",
		Short: "Mount or re-attach the FUSE filesystem daemon for an existing workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			mgr, err := config.NewManager(rootFlags.ConfigDir)
			if err != nil {
				return err
			}

			ws, err := mgr.Load(name)
			if err != nil {
				return err
			}

			if ws.NoFuse {
				return fmt.Errorf("workspace %q was created in --no-fuse mode (direct materialization) and does not use FUSE mounting", name)
			}

			if ws.PID > 0 && isProcessAlive(ws.PID) && isMountPoint(ws.MountDir) {
				fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q is already mounted and active (PID %d).\n", name, ws.PID)
				return nil
			}

			// Clean up any stale mount or zombie process if present
			if isMountPoint(ws.MountDir) {
				_ = unmountWorkspace(ws.MountDir)
			}
			if ws.PID > 0 && isProcessAlive(ws.PID) {
				terminateProcess(ws.PID)
			}

			if err := os.MkdirAll(ws.MountDir, 0755); err != nil {
				return fmt.Errorf("failed to ensure mount directory %s: %w", ws.MountDir, err)
			}

			nieblafsBin, err := findNieblafsBinary()
			if err != nil {
				return fmt.Errorf("nieblafs binary not found: %w", err)
			}

			_, cacheDir, err := provisionWorkspaceDirs(name)
			if err != nil {
				return fmt.Errorf("failed to resolve workspace cache dir: %w", err)
			}

			serverAddr := rootFlags.Server
			if serverAddr == "" {
				serverAddr = DefaultServerAddr
			}

			socketPath := ws.SocketPath
			if socketPath == "" {
				socketPath = filepath.Join(filepath.Dir(ws.ScratchDir), "nieblafs.sock")
			}

			pid, err := spawnNieblafs(nieblafsBin, ws.MountDir, ws.ScratchDir, cacheDir, ws.RootTreeHash, serverAddr, socketPath)
			if err != nil {
				return fmt.Errorf("failed to mount workspace %q: %w", name, err)
			}

			ws.PID = pid
			ws.SocketPath = socketPath
			if err := mgr.Save(ws); err != nil {
				return fmt.Errorf("failed to update workspace metadata: %w", err)
			}

			pollMountActive(cmd.OutOrStdout(), pid, ws.MountDir, socketPath, ws.ScratchDir)

			fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q successfully mounted at %s (PID %d).\n", name, ws.MountDir, pid)
			return nil
		},
	}

	return cmd
}

func newWorkspaceUnmountCmd(rootFlags *RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unmount <name>",
		Short: "Safely unmount the FUSE filesystem daemon for a workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			mgr, err := config.NewManager(rootFlags.ConfigDir)
			if err != nil {
				return err
			}

			ws, err := mgr.Load(name)
			if err != nil {
				return err
			}

			if ws.NoFuse {
				return fmt.Errorf("workspace %q was created in --no-fuse mode (direct materialization) and does not use FUSE mounting", name)
			}

			if isMountPoint(ws.MountDir) {
				if err := unmountWorkspace(ws.MountDir); err != nil {
					return fmt.Errorf("failed to unmount %s: %w", ws.MountDir, err)
				}
			}

			if ws.PID > 0 && isProcessAlive(ws.PID) {
				terminateProcess(ws.PID)
			}

			ws.PID = 0
			if err := mgr.Save(ws); err != nil {
				return fmt.Errorf("failed to update workspace metadata: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q successfully unmounted from %s.\n", name, ws.MountDir)
			return nil
		},
	}

	return cmd
}

func getCurrentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if env := os.Getenv("USER"); env != "" {
		return env
	}
	return "user"
}

func provisionWorkspaceDirs(name string) (scratchDir, cacheDir string, err error) {
	// Preferred cache: /niebla/cache -> fallback: ~/.cache/niebla/blobs
	cacheDir = "/niebla/cache"
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		home, hErr := os.UserHomeDir()
		if hErr != nil {
			return "", "", fmt.Errorf("cannot resolve home directory: %w", hErr)
		}
		cacheDir = filepath.Join(home, ".cache", "niebla", "blobs")
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create fallback cache directory %s: %w", cacheDir, err)
		}
	}

	// Preferred scratch: /scratch/niebla/workspaces/<name> -> fallback: ~/.niebla/workspaces/<name>/scratch
	scratchDir = filepath.Join("/scratch/niebla/workspaces", name)
	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		home, hErr := os.UserHomeDir()
		if hErr != nil {
			return "", "", fmt.Errorf("cannot resolve home directory: %w", hErr)
		}
		scratchDir = filepath.Join(home, ".niebla", "workspaces", name, "scratch")
		if err := os.MkdirAll(scratchDir, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create fallback scratch directory %s: %w", scratchDir, err)
		}
	}

	// Clean any previous stale contents so a fresh workspace starts empty
	_ = os.RemoveAll(scratchDir)
	_ = os.MkdirAll(scratchDir, 0755)

	return scratchDir, cacheDir, nil
}

func checkFUSEEnvironment(out io.Writer) {
	rep := preflight.VerifyFUSEReadiness()
	if !rep.Ready || len(rep.Warnings) > 0 {
		fmt.Fprintf(out, "Warning: FUSE environment prerequisites not fully satisfied:\n%s      Hint: Use '--no-fuse' for direct directory materialization without FUSE.\n", preflight.FormatDiagnostics(rep))
	}
}

func findNieblafsBinary() (string, error) {
	if env := os.Getenv("NIEBLAFS_BIN"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}

	// Check next to current niebla binary
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "nieblafs")
		if _, err := os.Stat(sibling); err == nil {
			return sibling, nil
		}
	}

	// Look in PATH
	return exec.LookPath("nieblafs")
}

func spawnNieblafs(nieblafsBin, mountDir, scratchDir, cacheDir, treeHash, serverAddr, socketPath string) (int, error) {
	logDir := filepath.Dir(scratchDir)
	logPath := filepath.Join(logDir, "nieblafs.log")

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return 0, fmt.Errorf("failed to create nieblafs log file %s: %w", logPath, err)
	}

	args := []string{
		"--mount", mountDir,
		"--scratch", scratchDir,
		"--cache", cacheDir,
		"--tree-hash", treeHash,
		"--server", serverAddr,
	}
	if socketPath != "" {
		args = append(args, "--socket", socketPath)
	}

	cmd := exec.Command(nieblafsBin, args...)

	// Detach process cleanly from CLI session
	detachProcess(cmd)

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return 0, fmt.Errorf("failed to launch nieblafs: %w", err)
	}

	_ = logFile.Close()
	return cmd.Process.Pid, nil
}

func pollMountActive(out io.Writer, pid int, mountDir, socketPath, scratchDir string) {
	deadline := time.Now().Add(3 * time.Second)
	logPath := filepath.Join(filepath.Dir(scratchDir), "nieblafs.log")

	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			tailMsg := readLogTail(logPath, 5)
			fmt.Fprintf(out, "Warning: nieblafs daemon (PID %d) exited prematurely.\n%s\n", pid, tailMsg)
			return
		}

		if socketPath != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			if err := control.NewClient(socketPath).Ping(ctx); err == nil {
				cancel()
				return
			}
			cancel()
		}

		if isMountPoint(mountDir) {
			return
		}

		time.Sleep(50 * time.Millisecond)
	}
}

func checkWorkspaceStatus(ws *config.WorkspaceConfig) string {
	if ws.NoFuse {
		if ws.MountDir != "" {
			if info, err := os.Stat(ws.MountDir); err == nil && info.IsDir() {
				return "ACTIVE (NO-FUSE)"
			}
		}
		return "STALE"
	}

	alive := ws.PID > 0 && isProcessAlive(ws.PID)
	mounted := isMountPoint(ws.MountDir)

	if alive && mounted {
		return "ACTIVE"
	}
	return "STALE"
}

func hasScratchModifications(scratchDir string) (bool, error) {
	if scratchDir == "" {
		return false, nil
	}
	entries, err := os.ReadDir(scratchDir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return len(entries) > 0, nil
}

func readLogTail(logPath string, lines int) string {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	allLines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(allLines) <= lines {
		return strings.Join(allLines, "\n")
	}
	return strings.Join(allLines[len(allLines)-lines:], "\n")
}
