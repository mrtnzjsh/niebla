package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"niebla/internal/cli/config"
	toolchainconfig "niebla/internal/config"
	"niebla/internal/groups"
	"niebla/pkg/cas"
	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"

	"github.com/spf13/cobra"
)

type uploadedBlob struct {
	SHA  []byte
	Size uint64
	Mode uint32
}

// ShelveOutput represents the machine-readable JSON output for 'niebla shelve --json'.
type ShelveOutput struct {
	Workspace    string `json:"workspace"`
	CLID         int64  `json:"cl_id"`
	ParentCL     int64  `json:"parent_cl"`
	Description  string `json:"description"`
	RootTreeHash string `json:"root_tree_hash"`
	FilesShelved int    `json:"files_shelved"`
}

func uploadChangesToCAS(ctx context.Context, client nieblav1.NieblaServiceClient, changes []FileChange) (map[string]uploadedBlob, error) {
	uploads := make(map[string]uploadedBlob)
	for _, change := range changes {
		if change.Type == ChangeDeleted {
			continue
		}

		blobInfo, err := uploadFileToCAS(ctx, client, change.DiskPath)
		if err != nil {
			return nil, fmt.Errorf("failed to upload blob for %s: %w", change.Path, err)
		}
		uploads[change.Path] = blobInfo
	}
	return uploads, nil
}

func resolveShelveReviewers(ctx context.Context, reviewers string) ([]string, error) {
	if strings.TrimSpace(reviewers) == "" {
		return nil, nil
	}
	toolchainCfg, err := toolchainconfig.Load()
	if err != nil {
		toolchainCfg = toolchainconfig.DefaultConfig()
	}
	resolver := groups.NewResolver(toolchainCfg.Critique.Groups)
	resolved, err := groups.ExpandReviewers(ctx, resolver, []string{reviewers})
	if err != nil {
		return nil, fmt.Errorf("failed to expand reviewers %q: %w", reviewers, err)
	}
	return resolved, nil
}

func executeShelve(
	cmd *cobra.Command,
	rootFlags *RootFlags,
	description string,
	parentCL, targetCL int64,
	newCL bool,
	reviewers string,
) error {
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("a changelist description is required (-m \"<description>\")")
	}

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
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
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

	if len(changes) == 0 {
		return fmt.Errorf("no changes to shelve in workspace %q", ws.Name)
	}

	uploads, err := uploadChangesToCAS(ctx, client, changes)
	if err != nil {
		return err
	}

	writeTree := func(treeHash []byte, entries []tree.Entry) error {
		manifest, err := tree.MarshalManifest(entries)
		if err != nil {
			return err
		}
		_, _, err = uploadBytesToCAS(ctx, client, manifest)
		return err
	}
	newRootTreeHash, err := BuildUpdatedTree(rootTreeHash, changes, uploads, fetchTree, writeTree)
	if err != nil {
		return fmt.Errorf("failed to compute updated Merkle tree: %w", err)
	}

	effectiveParentCL := parentCL
	if effectiveParentCL <= 0 {
		effectiveParentCL = ws.BaseCL
	}

	resolvedReviewers, err := resolveShelveReviewers(ctx, reviewers)
	if err != nil {
		return err
	}

	effectiveCLID := targetCL
	if !newCL && effectiveCLID <= 0 && ws.ActiveCL > 0 {
		effectiveCLID = ws.ActiveCL
	}

	author := getCurrentUsername()
	shelveResp, err := client.ShelveCL(ctx, &nieblav1.ShelveCLRequest{
		WorkspaceId:  ws.WorkspaceID,
		Author:       author,
		Description:  description,
		RootTreeHash: newRootTreeHash,
		ParentCl:     effectiveParentCL,
		Reviewers:    resolvedReviewers,
		ClId:         effectiveCLID,
	})
	if err != nil {
		return fmt.Errorf("server failed to shelve changelist: %w", err)
	}

	newTreeHex := hex.EncodeToString(newRootTreeHash)
	ws.ActiveCL = shelveResp.ClId
	ws.RootTreeHash = newTreeHex
	if err := mgr.Save(ws); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to update workspace active CL metadata: %v\n", err)
	}

	// Clean up whiteout / tombstone markers in scratch for successfully shelved deletions
	for _, change := range changes {
		if change.Type == ChangeDeleted && change.DiskPath != "" {
			_ = os.Remove(change.DiskPath)
		}
	}

	if rootFlags.JSON {
		return writeJSON(cmd.OutOrStdout(), ShelveOutput{
			Workspace:    ws.Name,
			CLID:         shelveResp.ClId,
			ParentCL:     effectiveParentCL,
			Description:  description,
			RootTreeHash: newTreeHex,
			FilesShelved: len(changes),
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Shelved CL %d: %q\n", shelveResp.ClId, description)
	return nil
}

// NewShelveCmd constructs the 'shelve' command.
func NewShelveCmd(rootFlags *RootFlags) *cobra.Command {
	var description string
	var parentCL int64
	var targetCL int64
	var newCL bool
	var reviewers string

	cmd := &cobra.Command{
		Use:   "shelve",
		Short: "Shelve pending workspace changes into a new Changelist",
		Long:  "Uploads modified/added scratch files to CAS, recomputes the Merkle tree, and creates a shelved CL.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return executeShelve(cmd, rootFlags, description, parentCL, targetCL, newCL, reviewers)
		},
	}

	cmd.Flags().StringVarP(&description, "message", "m", "", "Changelist description (required)")
	cmd.Flags().Int64Var(&parentCL, "on-cl", 0, "Parent changelist ID (defaults to workspace base CL)")
	cmd.Flags().Int64Var(&targetCL, "cl", 0, "Target existing changelist ID (defaults to active CL in workspace)")
	cmd.Flags().BoolVar(&newCL, "new-cl", false, "Force creating a new CL even if an active CL is set in workspace")
	cmd.Flags().StringVar(&reviewers, "reviewers", "", "Comma-separated list of reviewer usernames or @group names")

	return cmd
}

func uploadReaderToCAS(ctx context.Context, client nieblav1.NieblaServiceClient, r io.Reader) ([]byte, uint64, error) {
	stream, err := client.UploadBlob(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open UploadBlob stream: %w", err)
	}

	buf := make([]byte, cas.DefaultChunkSize)
	for {
		n, rErr := r.Read(buf)
		if n > 0 {
			if err := stream.Send(&nieblav1.UploadBlobRequest{Data: buf[:n]}); err != nil {
				return nil, 0, fmt.Errorf("failed to send blob chunk: %w", err)
			}
		}
		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				break
			}
			return nil, 0, fmt.Errorf("failed reading stream for upload: %w", rErr)
		}
	}

	resp, err := stream.CloseAndRecv()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to finish UploadBlob: %w", err)
	}

	return resp.Sha256, resp.Size, nil
}

func uploadBytesToCAS(ctx context.Context, client nieblav1.NieblaServiceClient, data []byte) ([]byte, uint64, error) {
	return uploadReaderToCAS(ctx, client, bytes.NewReader(data))
}

func uploadFileToCAS(ctx context.Context, client nieblav1.NieblaServiceClient, diskPath string) (uploadedBlob, error) {
	info, err := os.Stat(diskPath)
	if err != nil {
		return uploadedBlob{}, fmt.Errorf("failed to stat %s: %w", diskPath, err)
	}

	fileMode := uint32(0100644)
	if info.Mode()&0111 != 0 {
		fileMode = 0100755
	}

	f, err := os.Open(diskPath)
	if err != nil {
		return uploadedBlob{}, fmt.Errorf("failed to open %s: %w", diskPath, err)
	}
	defer f.Close()

	sha, size, err := uploadReaderToCAS(ctx, client, f)
	if err != nil {
		return uploadedBlob{}, err
	}

	return uploadedBlob{
		SHA:  sha,
		Size: size,
		Mode: fileMode,
	}, nil
}

type dirNode struct {
	files   map[string]*tree.Entry // nil entry represents deletion
	subdirs map[string]*dirNode
}

func newDirNode() *dirNode {
	return &dirNode{
		files:   make(map[string]*tree.Entry),
		subdirs: make(map[string]*dirNode),
	}
}

// BuildUpdatedTree incorporates dirty changes and uploaded blobs into the Merkle tree hierarchy.
func BuildUpdatedTree(
	baseRootTreeHash []byte,
	changes []FileChange,
	uploads map[string]uploadedBlob,
	fetchTree tree.FetchTreeFunc,
	writeTrees ...func(treeHash []byte, entries []tree.Entry) error,
) ([]byte, error) {
	rootNode := newDirNode()

	for _, change := range changes {
		parts := strings.Split(filepath.ToSlash(change.Path), "/")
		current := rootNode

		for i := 0; i < len(parts)-1; i++ {
			part := parts[i]
			if _, exists := current.subdirs[part]; !exists {
				current.subdirs[part] = newDirNode()
			}
			current = current.subdirs[part]
		}

		fileName := parts[len(parts)-1]
		if change.Type == ChangeDeleted {
			current.files[fileName] = nil
		} else {
			up := uploads[change.Path]
			current.files[fileName] = &tree.Entry{
				Name:       fileName,
				Mode:       up.Mode,
				Size:       up.Size,
				BlobSHA256: up.SHA,
			}
		}
	}

	var writeTree func(treeHash []byte, entries []tree.Entry) error
	if len(writeTrees) > 0 {
		writeTree = writeTrees[0]
	}

	return recursivelyUpdateTree(baseRootTreeHash, rootNode, fetchTree, writeTree)
}

func recursivelyUpdateTree(
	currentTreeHash []byte,
	mod *dirNode,
	fetchTree tree.FetchTreeFunc,
	writeTree func(treeHash []byte, entries []tree.Entry) error,
) ([]byte, error) {
	entryMap := make(map[string]tree.Entry)

	if len(currentTreeHash) > 0 && !bytes.Equal(currentTreeHash, tree.EmptyTreeHash) {
		baseEntries, err := fetchTree(currentTreeHash)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch base tree entries: %w", err)
		}
		for _, entry := range baseEntries {
			entryMap[entry.Name] = entry
		}
	}

	for name, fileEntry := range mod.files {
		if fileEntry == nil {
			delete(entryMap, name)
		} else {
			entryMap[name] = *fileEntry
		}
	}

	for subName, subMod := range mod.subdirs {
		var subBaseHash []byte
		if existing, ok := entryMap[subName]; ok && existing.IsDir() {
			subBaseHash = existing.SubTreeHash
		} else {
			subBaseHash = tree.EmptyTreeHash
		}

		newSubHash, err := recursivelyUpdateTree(subBaseHash, subMod, fetchTree, writeTree)
		if err != nil {
			return nil, err
		}

		if bytes.Equal(newSubHash, tree.EmptyTreeHash) {
			delete(entryMap, subName)
		} else {
			entryMap[subName] = tree.Entry{
				Name:        subName,
				Mode:        0040755,
				SubTreeHash: newSubHash,
			}
		}
	}

	entries := make([]tree.Entry, 0, len(entryMap))
	for _, entry := range entryMap {
		entries = append(entries, entry)
	}

	if len(entries) == 0 {
		return append([]byte(nil), tree.EmptyTreeHash...), nil
	}

	tree.SortEntries(entries)
	treeHash := tree.ComputeTreeHash(entries)
	if writeTree != nil {
		if err := writeTree(treeHash, entries); err != nil {
			return nil, fmt.Errorf("failed to persist tree manifest %x: %w", treeHash, err)
		}
	}
	return treeHash, nil
}

func computeSHA256(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}
