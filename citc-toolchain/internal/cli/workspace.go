package cli

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"citc-toolchain/internal/cli/config"
	citcv1 "citc-toolchain/proto/v1"

	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
)

// NewWorkspaceCmd creates the 'workspace' parent command and its subcommands.
func NewWorkspaceCmd(rootFlags *RootFlags) *cobra.Command {
	wsCmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage CitC virtual workspaces",
		Long:  "Commands to create, list, and delete virtual workspaces backed by CitC server and FUSE.",
	}

	wsCmd.AddCommand(newWorkspaceCreateCmd(rootFlags))
	wsCmd.AddCommand(newWorkspaceListCmd(rootFlags))
	wsCmd.AddCommand(newWorkspaceDeleteCmd(rootFlags))

	return wsCmd
}

func newWorkspaceCreateCmd(rootFlags *RootFlags) *cobra.Command {
	var baseCL int64
	var mountDir string

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create and mount a new CitC virtual workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			serverAddr := ResolveServerAddr(rootFlags.Server)

			mgr, err := config.NewManager(rootFlags.ConfigDir)
			if err != nil {
				return err
			}

			// Check if already exists locally
			if _, err := mgr.Load(name); err == nil {
				return fmt.Errorf("workspace %q already exists locally in %s", name, mgr.ConfigFilePath(name))
			}

			// 1. Call citc-server CreateWorkspace
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			conn, client, err := DialGRPC(ctx, serverAddr)
			if err != nil {
				return err
			}
			defer conn.Close()

			owner := getCurrentUsername()
			resp, err := client.CreateWorkspace(ctx, &citcv1.CreateWorkspaceRequest{
				Name:   name,
				Owner:  owner,
				BaseCl: baseCL,
			})
			if err != nil {
				return fmt.Errorf("server failed to create workspace %q: %w", name, err)
			}

			rootTreeHashHex := hex.EncodeToString(resp.RootTreeHash)

			// 2. Provision scratch, cache, and mount directories
			scratchDir, cacheDir, err := provisionWorkspaceDirs(name)
			if err != nil {
				return fmt.Errorf("failed to provision workspace directories: %w", err)
			}

			resolvedMountDir := mountDir
			if resolvedMountDir == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("failed to get user home directory: %w", err)
				}
				resolvedMountDir = filepath.Join(home, "citc", name)
			}
			resolvedMountDir, err = filepath.Abs(resolvedMountDir)
			if err != nil {
				return fmt.Errorf("failed to resolve absolute mount path: %w", err)
			}

			if err := os.MkdirAll(resolvedMountDir, 0755); err != nil {
				return fmt.Errorf("failed to create mount directory %s: %w", resolvedMountDir, err)
			}

			// Check /dev/fuse accessibility
			checkDevFuse()

			// 3. Spawn citcfs daemon in background
			var daemonPID int
			citcfsBin, err := findCitcfsBinary()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: citcfs binary not found in PATH or next to citc; FUSE daemon not started.\n")
			} else {
				pid, err := spawnCitcfs(citcfsBin, resolvedMountDir, scratchDir, cacheDir, rootTreeHashHex, serverAddr)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to spawn citcfs daemon: %v\n", err)
				} else {
					daemonPID = pid
				}
			}

			// 4. Save metadata
			wsConfig := &config.WorkspaceConfig{
				WorkspaceID:  resp.WorkspaceId,
				Name:         name,
				BaseCL:       resp.BaseCl,
				RootTreeHash: rootTreeHashHex,
				MountDir:     resolvedMountDir,
				ScratchDir:   scratchDir,
				PID:          daemonPID,
			}

			if err := mgr.Save(wsConfig); err != nil {
				return fmt.Errorf("failed to write workspace metadata: %w", err)
			}

			// 5. Poll for mount activation if citcfs was spawned
			if daemonPID > 0 {
				pollMountActive(cmd.OutOrStdout(), daemonPID, resolvedMountDir, scratchDir)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q created successfully.\n", name)
			fmt.Fprintf(cmd.OutOrStdout(), "  Base CL   : %d\n", resp.BaseCl)
			fmt.Fprintf(cmd.OutOrStdout(), "  Tree Hash : %s\n", rootTreeHashHex)
			fmt.Fprintf(cmd.OutOrStdout(), "  Mount Dir : %s\n", resolvedMountDir)
			fmt.Fprintf(cmd.OutOrStdout(), "  Scratch   : %s\n", scratchDir)
			if daemonPID > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "  Daemon PID: %d\n", daemonPID)
			}

			return nil
		},
	}

	cmd.Flags().Int64Var(&baseCL, "cl", 0, "Base Changelist ID to branch workspace from (0 = trunk head)")
	cmd.Flags().StringVar(&mountDir, "mount-dir", "", "Custom mount directory (defaults to ~/citc/<name>)")

	return cmd
}

func newWorkspaceListCmd(rootFlags *RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all CitC workspaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr, err := config.NewManager(rootFlags.ConfigDir)
			if err != nil {
				return err
			}

			workspaces, err := mgr.List()
			if err != nil {
				return err
			}

			if len(workspaces) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No CitC workspaces found.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 8, 4, ' ', 0)
			fmt.Fprintln(w, "NAME\tBASE CL\tMOUNT PATH\tSTATUS")

			for _, ws := range workspaces {
				status := checkWorkspaceStatus(ws)
				fmt.Fprintf(w, "%s\t%d\t%s\t%s\n", ws.Name, ws.BaseCL, ws.MountDir, status)
			}

			return w.Flush()
		},
	}
}

func newWorkspaceDeleteCmd(rootFlags *RootFlags) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a virtual workspace and unmount it",
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

			// Check for modifications if not forced
			if !force {
				hasDirty, err := hasScratchModifications(ws.ScratchDir)
				if err == nil && hasDirty {
					return fmt.Errorf("workspace %q has unshelved modifications in %s; use --force to delete anyway", name, ws.ScratchDir)
				}
			}

			// 1. Unmount workspace
			if isMountPoint(ws.MountDir) {
				if err := unmountWorkspace(ws.MountDir); err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: unmounting %s failed: %v\n", ws.MountDir, err)
				}
			}

			// 2. Terminate daemon PID if alive
			if ws.PID > 0 && isProcessAlive(ws.PID) {
				terminateProcess(ws.PID)
			}

			// 3. Remove scratch directory
			if ws.ScratchDir != "" {
				_ = os.RemoveAll(ws.ScratchDir)
				// Clean up parent directory if empty
				parent := filepath.Dir(ws.ScratchDir)
				_ = os.Remove(parent) // Only removes if empty
			}

			// 4. Delete config metadata
			if err := mgr.Delete(name); err != nil {
				return fmt.Errorf("failed to remove workspace metadata: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Workspace %q deleted successfully.\n", name)
			return nil
		},
	}

	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force deletion even with unshelved modifications")

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
	// Preferred cache: /citc/cache -> fallback: ~/.cache/citc/blobs
	cacheDir = "/citc/cache"
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		home, hErr := os.UserHomeDir()
		if hErr != nil {
			return "", "", fmt.Errorf("cannot resolve home directory: %w", hErr)
		}
		cacheDir = filepath.Join(home, ".cache", "citc", "blobs")
		if err := os.MkdirAll(cacheDir, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create fallback cache directory %s: %w", cacheDir, err)
		}
	}

	// Preferred scratch: /citc/workspaces/<name>/scratch -> fallback: ~/.citc/workspaces/<name>/scratch
	scratchDir = filepath.Join("/citc/workspaces", name, "scratch")
	if err := os.MkdirAll(scratchDir, 0755); err != nil {
		home, hErr := os.UserHomeDir()
		if hErr != nil {
			return "", "", fmt.Errorf("cannot resolve home directory: %w", hErr)
		}
		scratchDir = filepath.Join(home, ".citc", "workspaces", name, "scratch")
		if err := os.MkdirAll(scratchDir, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create fallback scratch directory %s: %w", scratchDir, err)
		}
	}

	// Clean any previous stale contents so a fresh workspace starts empty
	_ = os.RemoveAll(scratchDir)
	_ = os.MkdirAll(scratchDir, 0755)

	return scratchDir, cacheDir, nil
}

func checkDevFuse() {
	if f, err := os.Open("/dev/fuse"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: /dev/fuse is not accessible (%v). FUSE mounting may fail.\n", err)
	} else {
		_ = f.Close()
	}
}

func findCitcfsBinary() (string, error) {
	if env := os.Getenv("CITCFS_BIN"); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}

	// Check next to current citc binary
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), "citcfs")
		if _, err := os.Stat(sibling); err == nil {
			return sibling, nil
		}
	}

	// Look in PATH
	return exec.LookPath("citcfs")
}

func spawnCitcfs(citcfsBin, mountDir, scratchDir, cacheDir, treeHash, serverAddr string) (int, error) {
	logDir := filepath.Dir(scratchDir)
	logPath := filepath.Join(logDir, "citcfs.log")

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return 0, fmt.Errorf("failed to create citcfs log file %s: %w", logPath, err)
	}

	cmd := exec.Command(citcfsBin,
		"--mount", mountDir,
		"--scratch", scratchDir,
		"--cache", cacheDir,
		"--tree-hash", treeHash,
		"--server", serverAddr,
	)

	// Detach process cleanly from CLI session
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return 0, fmt.Errorf("failed to launch citcfs: %w", err)
	}

	_ = logFile.Close()
	return cmd.Process.Pid, nil
}

func pollMountActive(out io.Writer, pid int, mountDir, scratchDir string) {
	deadline := time.Now().Add(3 * time.Second)
	logPath := filepath.Join(filepath.Dir(scratchDir), "citcfs.log")

	for time.Now().Before(deadline) {
		// 1. Check if process is still alive
		if !isProcessAlive(pid) {
			tailMsg := readLogTail(logPath, 5)
			fmt.Fprintf(out, "Warning: citcfs daemon (PID %d) exited prematurely.\n%s\n", pid, tailMsg)
			return
		}

		// 2. Check if mounted
		if isMountPoint(mountDir) {
			return
		}

		time.Sleep(100 * time.Millisecond)
	}
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Check /proc/<pid> on Linux
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", pid)); err == nil {
		return true
	}
	// Fallback to signal 0
	return syscall.Kill(pid, 0) == nil
}

func terminateProcess(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

func isMountPoint(dir string) bool {
	cleanDir := filepath.Clean(dir)
	f, err := os.Open("/proc/mounts")
	if err != nil {
		// Fallback to checking via unix.Access
		return unix.Access(cleanDir, unix.F_OK) == nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 {
			if filepath.Clean(fields[1]) == cleanDir {
				return true
			}
		}
	}
	return false
}

func unmountWorkspace(mountDir string) error {
	// Try fusermount3 first
	if fuserPath, err := exec.LookPath("fusermount3"); err == nil {
		cmd := exec.Command(fuserPath, "-u", mountDir)
		if output, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else {
			_ = output
		}
	}

	// Try fusermount
	if fuserPath, err := exec.LookPath("fusermount"); err == nil {
		cmd := exec.Command(fuserPath, "-u", mountDir)
		if output, err := cmd.CombinedOutput(); err == nil {
			return nil
		} else {
			_ = output
		}
	}

	// Direct syscall unmount
	if err := unix.Unmount(mountDir, 0); err == nil {
		return nil
	}

	return unix.Unmount(mountDir, unix.MNT_FORCE)
}

func checkWorkspaceStatus(ws *config.WorkspaceConfig) string {
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
