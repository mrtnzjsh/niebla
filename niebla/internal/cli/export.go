package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"niebla/pkg/cas"
	"niebla/pkg/materialize"
	nieblav1 "niebla/pkg/proto/nieblav1"

	"github.com/spf13/cobra"
)

// NewExportCmd constructs the 'export' command for stateless FUSE-less tree materialization.
func NewExportCmd(rootFlags *RootFlags) *cobra.Command {
	var targetCL int64
	var patchset int32
	var destDir string
	var concurrency int
	var casDir string

	cmd := &cobra.Command{
		Use:   "export",
		Short: "Materialize a changelist's Merkle tree directly into a directory without FUSE",
		Long:  "Fetches the Merkle tree and blobs for a changelist (or trunk head) and materializes them directly into --dest.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(destDir) == "" {
				return fmt.Errorf("--dest <dir> is required")
			}

			absDest, err := filepath.Abs(destDir)
			if err != nil {
				return fmt.Errorf("failed to resolve destination path %q: %w", destDir, err)
			}

			serverAddr := ResolveServerAddr(rootFlags.Server)
			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			conn, client, err := DialGRPC(ctx, serverAddr)
			if err != nil {
				return err
			}
			defer conn.Close()

			var resolvedCL int64
			var resolvedPS int32
			var rootTreeHash []byte

			if targetCL <= 0 {
				headResp, err := client.GetHeadCL(ctx, &nieblav1.GetHeadCLRequest{})
				if err != nil {
					return fmt.Errorf("failed to query head CL: %w", err)
				}
				resolvedCL = headResp.ClId
				resolvedPS = 1
				rootTreeHash = headResp.RootTreeHash
			} else {
				clResp, err := client.GetCL(ctx, &nieblav1.GetCLRequest{ClId: targetCL})
				if err != nil {
					return fmt.Errorf("failed to query CL %d: %w", targetCL, err)
				}
				resolvedCL = clResp.ClId
				resolvedPS = clResp.CurrentPatchset
				if patchset > 0 {
					resolvedPS = patchset
				}
				rootTreeHash = clResp.RootTreeHash
			}

			var hardlinkCAS *cas.Storage
			if casDir != "" {
				s, err := cas.NewStorage(casDir)
				if err != nil {
					return fmt.Errorf("failed to open CAS directory %q: %w", casDir, err)
				}
				hardlinkCAS = s
			}

			fetchTree := makeTreeFetcher(ctx, client)
			fetchBlob := makeBlobFetcher(client, defaultBlobCacheDirs())

			stats, err := materialize.MaterializeTree(ctx, rootTreeHash, absDest, fetchTree, fetchBlob, materialize.Options{
				Concurrency: concurrency,
				HardlinkCAS: hardlinkCAS,
			})
			if err != nil {
				return fmt.Errorf("failed to export CL %d to %s: %w", resolvedCL, absDest, err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Exported CL %d (patchset %d, tree %s) to %s (%d files, %d dirs, %d bytes).\n",
				resolvedCL,
				resolvedPS,
				hex.EncodeToString(rootTreeHash),
				absDest,
				stats.FilesMaterialized,
				stats.DirectoriesCreated,
				stats.BytesWritten,
			)
			return nil
		},
	}

	cmd.Flags().Int64Var(&targetCL, "cl", 0, "Changelist ID to export (0 = trunk head)")
	cmd.Flags().Int32Var(&patchset, "patchset", 0, "Patchset number to export (0 = latest patchset)")
	cmd.Flags().StringVar(&destDir, "dest", "", "Destination directory to materialize files into (required)")
	cmd.Flags().IntVar(&concurrency, "concurrency", materialize.DefaultConcurrency, "Concurrent worker pool size")
	cmd.Flags().StringVar(&casDir, "cas-dir", "", "Optional local CAS directory for zero-copy hardlink materialization")

	return cmd
}

func defaultBlobCacheDirs() []string {
	dirs := []string{"/niebla/cache"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".cache", "niebla", "blobs"))
	}
	return dirs
}

func makeBlobFetcher(client nieblav1.NieblaServiceClient, cacheDirs []string) materialize.BlobFetcher {
	return func(ctx context.Context, sha256Bytes []byte, w io.Writer) error {
		shaHex := hex.EncodeToString(sha256Bytes)

		// 1. Check local CAS caches first
		for _, cacheDir := range cacheDirs {
			if casStorage, err := cas.NewStorage(cacheDir); err == nil {
				if f, _, openErr := casStorage.OpenBlob(sha256Bytes); openErr == nil {
					_, copyErr := io.Copy(w, f)
					_ = f.Close()
					if copyErr == nil {
						return nil
					}
				}
			}
			flatPath := filepath.Join(cacheDir, shaHex)
			if f, err := os.Open(flatPath); err == nil {
				_, copyErr := io.Copy(w, f)
				_ = f.Close()
				if copyErr == nil {
					return nil
				}
			}
		}

		// 2. Stream via gRPC ReadBlob
		stream, err := client.ReadBlob(ctx, &nieblav1.ReadBlobRequest{
			Sha256: sha256Bytes,
			Offset: 0,
			Length: 0,
		})
		if err != nil {
			return fmt.Errorf("ReadBlob gRPC call failed for %s: %w", shaHex, err)
		}

		for {
			chunk, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return fmt.Errorf("error reading blob stream for %s: %w", shaHex, err)
			}
			if len(chunk.Data) > 0 {
				if _, wErr := w.Write(chunk.Data); wErr != nil {
					return wErr
				}
			}
		}

		return nil
	}
}
