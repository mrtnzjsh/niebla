package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"niebla/pkg/cas"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"github.com/spf13/cobra"
)

// CASPutOutput represents the JSON output for 'niebla cas put --json'.
type CASPutOutput struct {
	SHA256    string `json:"sha256"`
	SizeBytes uint64 `json:"size_bytes"`
	Path      string `json:"path"`
}

// CASGetOutput represents the JSON output for 'niebla cas get --json'.
type CASGetOutput struct {
	SHA256    string `json:"sha256"`
	SizeBytes uint64 `json:"size_bytes"`
	Path      string `json:"path"`
}

// NewCASCmd constructs the 'cas' command.
func NewCASCmd(rootFlags *RootFlags) *cobra.Command {
	casCmd := &cobra.Command{
		Use:   "cas",
		Short: "Interact with Content-Addressable Storage (CAS)",
		Long:  "Upload and download raw content-addressed blobs from the central Niebla CAS backend.",
	}

	casCmd.AddCommand(newCASPutCmd(rootFlags))
	casCmd.AddCommand(newCASGetCmd(rootFlags))
	casCmd.AddCommand(newCASGCCmd(rootFlags))

	return casCmd
}

func newCASPutCmd(rootFlags *RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "put <file>",
		Short: "Upload a file directly to central CAS storage",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := args[0]
			f, err := os.Open(filePath)
			if err != nil {
				return fmt.Errorf("failed to open file %s: %w", filePath, err)
			}
			defer f.Close()

			info, err := f.Stat()
			if err != nil {
				return fmt.Errorf("failed to stat file %s: %w", filePath, err)
			}
			if info.IsDir() {
				return fmt.Errorf("cannot put directory %s: 'niebla cas put' expects a single file", filePath)
			}

			serverAddr := ResolveServerAddr(rootFlags.Server)
			ctx, cancel := context.WithTimeout(cmd.Context(), 120*time.Second)
			defer cancel()

			conn, client, err := DialGRPC(ctx, serverAddr)
			if err != nil {
				return err
			}
			defer conn.Close()

			shaBytes, size, err := streamFileToCAS(ctx, client, f)
			if err != nil {
				return err
			}

			shaHex := hex.EncodeToString(shaBytes)

			if rootFlags.JSON {
				return writeJSON(cmd.OutOrStdout(), CASPutOutput{
					SHA256:    shaHex,
					SizeBytes: size,
					Path:      filePath,
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Uploaded %s (%d bytes)\nSHA-256: %s\n", filePath, size, shaHex)
			return nil
		},
	}

	return cmd
}

func newCASGetCmd(rootFlags *RootFlags) *cobra.Command {
	var outPath string

	cmd := &cobra.Command{
		Use:   "get <sha256>",
		Short: "Download a blob from CAS storage by SHA-256",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			shaHex := strings.TrimSpace(args[0])
			shaBytes, err := hex.DecodeString(shaHex)
			if err != nil || len(shaBytes) != 32 {
				return fmt.Errorf("invalid sha256 hex string: expected 64 hex characters (32 bytes)")
			}

			serverAddr := ResolveServerAddr(rootFlags.Server)
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			conn, client, err := DialGRPC(ctx, serverAddr)
			if err != nil {
				return err
			}
			defer conn.Close()

			stream, err := client.ReadBlob(ctx, &nieblav1.ReadBlobRequest{
				Sha256: shaBytes,
			})
			if err != nil {
				return fmt.Errorf("failed to read blob %s: %w", shaHex, err)
			}

			var dest io.Writer = cmd.OutOrStdout()
			var totalSize uint64

			if outPath != "" {
				f, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
				if err != nil {
					return fmt.Errorf("failed to create destination file %s: %w", outPath, err)
				}
				defer f.Close()
				dest = f
			}

			for {
				chunk, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					return fmt.Errorf("failed while streaming blob: %w", err)
				}
				if len(chunk.Data) > 0 {
					n, err := dest.Write(chunk.Data)
					if err != nil {
						return fmt.Errorf("failed to write output: %w", err)
					}
					totalSize += uint64(n)
				}
			}

			if rootFlags.JSON {
				return writeJSON(cmd.OutOrStdout(), CASGetOutput{
					SHA256:    shaHex,
					SizeBytes: totalSize,
					Path:      outPath,
				})
			}

			if outPath != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Downloaded %s (%d bytes) to %s\n", shaHex, totalSize, outPath)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&outPath, "out", "o", "", "Destination file path (defaults to stdout)")
	return cmd
}

func streamFileToCAS(ctx context.Context, client nieblav1.NieblaServiceClient, f io.Reader) ([]byte, uint64, error) {
	stream, err := client.UploadBlob(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open UploadBlob stream: %w", err)
	}

	buf := make([]byte, cas.DefaultChunkSize)
	for {
		n, rErr := f.Read(buf)
		if n > 0 {
			if err := stream.Send(&nieblav1.UploadBlobRequest{Data: buf[:n]}); err != nil {
				return nil, 0, fmt.Errorf("failed to send blob chunk: %w", err)
			}
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				break
			}
			return nil, 0, fmt.Errorf("failed to read file: %w", rErr)
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to finish UploadBlob: %w", err)
	}

	return resp.Sha256, resp.Size, nil
}

func newCASGCCmd(rootFlags *RootFlags) *cobra.Command {
	var gracePeriodStr string
	var dryRun bool
	var casDir string

	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Garbage collect unreferenced blobs from local or server CAS storage",
		Long:  "Scans CAS blobs, compares against active changelist and workspace trees, and removes unreferenced blobs older than the grace period.",
		RunE: func(cmd *cobra.Command, args []string) error {
			gracePeriod := 24 * time.Hour
			if gracePeriodStr != "" {
				parsed, err := time.ParseDuration(gracePeriodStr)
				if err != nil {
					return fmt.Errorf("invalid --grace-period duration %q: %w", gracePeriodStr, err)
				}
				gracePeriod = parsed
			}

			if casDir == "" {
				casDir = defaultBlobCacheDirs()[0]
			}

			storage, err := cas.NewStorage(casDir)
			if err != nil {
				return fmt.Errorf("failed to open CAS storage at %s: %w", casDir, err)
			}

			liveSet := make(map[string]bool)
			serverAddr := ResolveServerAddr(rootFlags.Server)
			dialCtx, dialCancel := context.WithTimeout(cmd.Context(), 500*time.Millisecond)
			conn, client, err := DialGRPC(dialCtx, serverAddr)
			if err == nil {
				defer conn.Close()
				headResp, headErr := client.GetHeadCL(dialCtx, &nieblav1.GetHeadCLRequest{})
				if headErr == nil && len(headResp.RootTreeHash) > 0 {
					var walk func(treeHash []byte)
					walk = func(treeHash []byte) {
						treeResp, tErr := client.GetTree(dialCtx, &nieblav1.GetTreeRequest{TreeHash: treeHash})
						if tErr == nil && treeResp != nil {
							for _, entry := range treeResp.Entries {
								if len(entry.BlobSha256) > 0 {
									liveSet[hex.EncodeToString(entry.BlobSha256)] = true
								}
								if len(entry.SubTreeHash) > 0 {
									walk(entry.SubTreeHash)
								}
							}
						}
					}
					walk(headResp.RootTreeHash)
				}
			}
			dialCancel()

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			stats, err := storage.CollectGarbage(ctx, liveSet, cas.GCOptions{
				GracePeriod: gracePeriod,
				DryRun:      dryRun,
			}, nil)
			if err != nil {
				return err
			}

			if rootFlags.JSON {
				return writeJSON(cmd.OutOrStdout(), stats)
			}

			if dryRun {
				fmt.Fprintln(cmd.OutOrStdout(), "[Dry Run] CAS Garbage Collection Preview:")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "✓ CAS Garbage Collection Completed:")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  • Total Scanned Blobs : %d\n", stats.TotalScannedBlobs)
			fmt.Fprintf(cmd.OutOrStdout(), "  • Live Blobs Preserved: %d\n", stats.LiveBlobs)
			fmt.Fprintf(cmd.OutOrStdout(), "  • Unreferenced Blobs  : %d\n", stats.UnreferencedBlobs)
			fmt.Fprintf(cmd.OutOrStdout(), "  • In-Grace Blobs Kept : %d\n", stats.SkippedGraceBlobs)
			if !dryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "  • Deleted Blobs       : %d\n", stats.DeletedBlobs)
				fmt.Fprintf(cmd.OutOrStdout(), "  • Disk Space Reclaimed: %d bytes\n", stats.BytesReclaimed)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "  • Disk Space to Purge : %d bytes\n", stats.BytesReclaimed)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&gracePeriodStr, "grace-period", "24h", "Grace period for newly written unreferenced blobs (e.g. 24h, 72h)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview blobs to be collected without deleting files")
	cmd.Flags().StringVar(&casDir, "dir", "", "Target CAS storage directory (defaults to local CAS cache)")

	return cmd
}
