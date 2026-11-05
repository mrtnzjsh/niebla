package cli

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"niebla/internal/cli/config"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/spf13/cobra"
)

// NewDiffCmd constructs the 'diff' command.
func NewDiffCmd(rootFlags *RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff [path]",
		Short: "Show unified diff of workspace changes against base CL",
		Long:  "Compares modified, added, and deleted files in the scratch overlay against their base CL versions.",
		Args:  cobra.MaximumNArgs(1),
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

			rootTreeHash, err := hex.DecodeString(ws.RootTreeHash)
			if err != nil {
				return fmt.Errorf("invalid root tree hash in workspace config: %w", err)
			}

			fetchTree := makeTreeFetcher(ctx, client)
			changes, err := ScanWorkspaceChanges(ws, rootTreeHash, fetchTree)
			if err != nil {
				return fmt.Errorf("failed to scan workspace changes: %w", err)
			}

			// Filter by target path if specified
			var targetFilter string
			if len(args) == 1 {
				targetFilter = normalizeTargetFilter(args[0], cwd, ws.MountDir)
			}

			cacheDirs := append([]string{"/niebla/cache"}, defaultBlobCacheDirs()...)

			for _, change := range changes {
				if targetFilter != "" && !matchesFilter(change.Path, targetFilter) {
					continue
				}

				diffText, err := computeFileDiff(ctx, client, cacheDirs, change)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to diff %s: %v\n", change.Path, err)
					continue
				}

				renderColoredDiff(cmd.OutOrStdout(), diffText)
			}

			return nil
		},
	}

	return cmd
}

func normalizeTargetFilter(arg, cwd, mountDir string) string {
	// If relative to cwd, resolve relative to mountDir
	if !filepath.IsAbs(arg) {
		absPath := filepath.Join(cwd, arg)
		if rel, err := filepath.Rel(mountDir, absPath); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	} else {
		if rel, err := filepath.Rel(mountDir, arg); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(strings.Trim(arg, "/"))
}

func matchesFilter(path, filter string) bool {
	if path == filter {
		return true
	}
	prefix := filter + "/"
	return strings.HasPrefix(path, prefix)
}

func computeFileDiff(ctx context.Context, client nieblav1.NieblaServiceClient, cacheDirs []string, change FileChange) (string, error) {
	var originalData []byte
	var newData []byte
	var fromFile string
	var toFile string

	switch change.Type {
	case ChangeAdded:
		fromFile = "/dev/null"
		toFile = "b/" + change.Path
		var err error
		newData, err = os.ReadFile(change.DiskPath)
		if err != nil {
			return "", fmt.Errorf("failed to read added file %s: %w", change.DiskPath, err)
		}

	case ChangeDeleted:
		fromFile = "a/" + change.Path
		toFile = "/dev/null"
		if change.BaseEntry != nil && len(change.BaseEntry.BlobSHA256) > 0 {
			var err error
			originalData, err = fetchBlobContent(ctx, client, cacheDirs, change.BaseEntry.BlobSHA256)
			if err != nil {
				return "", fmt.Errorf("failed to fetch base blob for %s: %w", change.Path, err)
			}
		}

	case ChangeModified:
		fromFile = "a/" + change.Path
		toFile = "b/" + change.Path
		if change.BaseEntry != nil && len(change.BaseEntry.BlobSHA256) > 0 {
			var err error
			originalData, err = fetchBlobContent(ctx, client, cacheDirs, change.BaseEntry.BlobSHA256)
			if err != nil {
				return "", fmt.Errorf("failed to fetch base blob for %s: %w", change.Path, err)
			}
		}
		var err error
		newData, err = os.ReadFile(change.DiskPath)
		if err != nil {
			return "", fmt.Errorf("failed to read modified file %s: %w", change.DiskPath, err)
		}
	}

	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(originalData)),
		B:        difflib.SplitLines(string(newData)),
		FromFile: fromFile,
		ToFile:   toFile,
		Context:  3,
	}

	return difflib.GetUnifiedDiffString(diff)
}

func fetchBlobContent(ctx context.Context, client nieblav1.NieblaServiceClient, cacheDirs []string, sha256Bytes []byte) ([]byte, error) {
	var buf bytes.Buffer
	fetcher := makeBlobFetcher(client, cacheDirs)
	if err := fetcher(ctx, sha256Bytes, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderColoredDiff(out io.Writer, diffText string) {
	if diffText == "" {
		return
	}

	useColor := isColorEnabled(out)
	lines := strings.Split(diffText, "\n")

	for _, line := range lines {
		if line == "" {
			continue
		}

		if !useColor {
			fmt.Fprintln(out, line)
			continue
		}

		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			fmt.Fprintf(out, "\033[1m%s\033[0m\n", line) // Bold
		} else if strings.HasPrefix(line, "@@") {
			fmt.Fprintf(out, "\033[36m%s\033[0m\n", line) // Cyan
		} else if strings.HasPrefix(line, "+") {
			fmt.Fprintf(out, "\033[32m%s\033[0m\n", line) // Green
		} else if strings.HasPrefix(line, "-") {
			fmt.Fprintf(out, "\033[31m%s\033[0m\n", line) // Red
		} else {
			fmt.Fprintln(out, line)
		}
	}
}
