package grpc

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"

	internalagents "niebla/internal/agents"
	"niebla/internal/config"
	"niebla/internal/db"
	internalevents "niebla/internal/events"
	"niebla/pkg/agents"
	"niebla/pkg/cas"
	pkgevents "niebla/pkg/events"
	"niebla/pkg/owners"
	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"
	"os"

	lru "github.com/hashicorp/golang-lru/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// DefaultTreeCacheSize is the default number of tree_hash entries cached in memory.
	DefaultTreeCacheSize = 10000
)

// Server implements the nieblav1.NieblaServiceServer interface.
type Server struct {
	nieblav1.UnimplementedNieblaServiceServer

	db              *db.DB
	casStorage      *cas.Storage
	treeCache       *lru.Cache[string, []*nieblav1.NodeEntry]
	toolchainConfig *config.Config
	emitter         pkgevents.Emitter
	rebaseQueue     *RebaseQueue
}

// NewServer creates a new instance of the NieblaService gRPC server.
func NewServer(database *db.DB, casStorage *cas.Storage, cacheSize int, configs ...*config.Config) (*Server, error) {
	if cacheSize <= 0 {
		cacheSize = DefaultTreeCacheSize
	}

	cache, err := lru.New[string, []*nieblav1.NodeEntry](cacheSize)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize tree LRU cache: %w", err)
	}

	var toolchainCfg *config.Config
	if len(configs) > 0 && configs[0] != nil {
		toolchainCfg = configs[0]
	} else if loaded, err := config.Load(); err == nil {
		toolchainCfg = loaded
	}

	var emitter pkgevents.Emitter = pkgevents.NewNoopEmitter()
	if toolchainCfg != nil && toolchainCfg.Events.Enabled {
		if em, err := internalevents.NewEmitterFromConfig(&toolchainCfg.Events); err == nil {
			emitter = em
		}
	}

	s := &Server{
		db:              database,
		casStorage:      casStorage,
		treeCache:       cache,
		toolchainConfig: toolchainCfg,
		emitter:         emitter,
	}
	s.rebaseQueue = NewRebaseQueue(2, 256, s.CascadeStackRebase)

	return s, nil
}

// Close stops the server's background workers and releases resources.
func (s *Server) Close() {
	if s.rebaseQueue != nil {
		s.rebaseQueue.Stop()
	}
}

// RebaseQueue returns the background rebase queue (used in testing).
func (s *Server) RebaseQueue() *RebaseQueue {
	return s.rebaseQueue
}

// Register registers the server with a gRPC server instance.
func (s *Server) Register(grpcServer *grpc.Server) {
	nieblav1.RegisterNieblaServiceServer(grpcServer, s)
}

// SetToolchainConfig updates the active toolchain configuration for review and submit guards.
func (s *Server) SetToolchainConfig(cfg *config.Config) {
	s.toolchainConfig = cfg
}

// SetEmitter updates the CloudEvents emitter instance.
func (s *Server) SetEmitter(emitter pkgevents.Emitter) {
	if emitter == nil {
		s.emitter = pkgevents.NewNoopEmitter()
		return
	}
	s.emitter = emitter
}

// getTreeEntries queries tree entries from PostgreSQL, falling back to CAS manifest blobs if uploaded by CLI shelve.
func (s *Server) getTreeEntries(ctx context.Context, treeHash []byte) ([]tree.Entry, error) {
	dbEntries, err := s.db.GetTreeEntries(ctx, treeHash)
	if err != nil {
		return nil, err
	}
	if len(dbEntries) > 0 {
		return dbEntries, nil
	}
	if s.casStorage != nil && s.casStorage.Exists(treeHash) {
		f, _, openErr := s.casStorage.OpenBlob(treeHash)
		if openErr == nil {
			data, readErr := io.ReadAll(f)
			_ = f.Close()
			if readErr == nil {
				if entries, uErr := tree.UnmarshalManifest(data); uErr == nil && len(entries) > 0 {
					_ = s.db.InsertTreeNodes(ctx, treeHash, entries)
					return entries, nil
				}
			}
		}
	}
	return dbEntries, nil
}

// GetTree returns directory entries for a given tree hash, utilizing an LRU cache and supporting pagination.
func (s *Server) GetTree(ctx context.Context, req *nieblav1.GetTreeRequest) (*nieblav1.GetTreeResponse, error) {
	if len(req.TreeHash) == 0 || bytes.Equal(req.TreeHash, tree.EmptyTreeHash) {
		return &nieblav1.GetTreeResponse{Entries: []*nieblav1.NodeEntry{}}, nil
	}

	cacheKey := hex.EncodeToString(req.TreeHash)

	var protoEntries []*nieblav1.NodeEntry
	// Check hot LRU cache first
	if cachedEntries, ok := s.treeCache.Get(cacheKey); ok {
		protoEntries = cachedEntries
	} else {
		// Cache miss: fetch from PostgreSQL (or CAS manifest fallback)
		dbEntries, err := s.getTreeEntries(ctx, req.TreeHash)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to query tree: %v", err)
		}

		protoEntries = make([]*nieblav1.NodeEntry, len(dbEntries))
		for i, e := range dbEntries {
			protoEntries[i] = e.ToProto()
		}

		s.treeCache.Add(cacheKey, protoEntries)
	}

	return paginateTreeEntries(protoEntries, req.PageSize, req.PageToken)
}

// GetNode retrieves metadata for a single named child node within a parent directory tree.
func (s *Server) GetNode(ctx context.Context, req *nieblav1.GetNodeRequest) (*nieblav1.NodeEntry, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "entry name cannot be empty")
	}

	cacheKey := hex.EncodeToString(req.ParentTreeHash)
	if cachedEntries, ok := s.treeCache.Get(cacheKey); ok {
		for _, e := range cachedEntries {
			if e.Name == req.Name {
				return e, nil
			}
		}
		return nil, status.Errorf(codes.NotFound, "node %q not found in parent tree %s", req.Name, cacheKey)
	}

	// Query from PostgreSQL (or CAS manifest fallback)
	entries, err := s.getTreeEntries(ctx, req.ParentTreeHash)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to query node: %v", err)
	}
	for _, e := range entries {
		if e.Name == req.Name {
			return e.ToProto(), nil
		}
	}

	return nil, status.Errorf(codes.NotFound, "node %q not found in parent tree %s", req.Name, cacheKey)
}

// ReadBlob streams content chunks directly from partitioned CAS storage to gRPC.
func (s *Server) ReadBlob(req *nieblav1.ReadBlobRequest, stream nieblav1.NieblaService_ReadBlobServer) error {
	if len(req.Sha256) != 32 {
		return status.Errorf(codes.InvalidArgument, "invalid sha256: expected 32 bytes, got %d", len(req.Sha256))
	}

	if !s.casStorage.Exists(req.Sha256) {
		return status.Errorf(codes.NotFound, "blob %s not found in CAS storage", hex.EncodeToString(req.Sha256))
	}

	err := s.casStorage.StreamBlob(
		req.Sha256,
		req.Offset,
		req.Length,
		cas.DefaultChunkSize,
		func(chunk []byte) error {
			return stream.Send(&nieblav1.BlobChunk{Data: chunk})
		},
	)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return status.Errorf(codes.Internal, "error streaming blob: %v", err)
	}

	return nil
}

// GetBlob streams content chunks directly from partitioned CAS storage to gRPC (implements NieblaService GetBlob).
func (s *Server) GetBlob(req *nieblav1.GetBlobRequest, stream nieblav1.NieblaService_GetBlobServer) error {
	return s.ReadBlob(&nieblav1.ReadBlobRequest{
		Sha256: req.Sha256,
		Offset: req.Offset,
		Length: req.Length,
	}, stream)
}

// UploadBlob handles streaming uploads to CAS storage with simultaneous SHA-256 computation
// and records the completed blob into PostgreSQL.
func (s *Server) UploadBlob(stream nieblav1.NieblaService_UploadBlobServer) error {
	writer, err := s.casStorage.BeginWriter()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to initialize blob writer: %v", err)
	}
	defer writer.Abort()

	for {
		req, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return status.Errorf(codes.Aborted, "stream interrupted: %v", err)
		}

		if len(req.Data) > 0 {
			if _, err := writer.Write(req.Data); err != nil {
				return status.Errorf(codes.Internal, "failed to write blob chunk: %v", err)
			}
		}
	}

	hashBytes, _, size, storagePath, err := writer.Commit()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to commit blob to storage: %v", err)
	}

	// Register blob record in PostgreSQL
	ctx := stream.Context()
	if err := s.db.InsertBlob(ctx, hashBytes, size, storagePath); err != nil {
		return status.Errorf(codes.Internal, "failed to record blob in database: %v", err)
	}

	return stream.SendAndClose(&nieblav1.UploadBlobResponse{
		Sha256: hashBytes,
		Size:   uint64(size),
	})
}

// CreateWorkspace provisions a new workspace mapped to base_cl (or current head if 0).
func (s *Server) CreateWorkspace(ctx context.Context, req *nieblav1.CreateWorkspaceRequest) (*nieblav1.CreateWorkspaceResponse, error) {
	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "workspace name cannot be empty")
	}
	if req.Owner == "" {
		return nil, status.Error(codes.InvalidArgument, "workspace owner cannot be empty")
	}

	ws, rootTreeHash, err := s.db.CreateWorkspace(ctx, req.Name, req.Owner, req.BaseCl)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create workspace: %v", err)
	}

	var baseCL int64
	if ws.BaseCL != nil {
		baseCL = *ws.BaseCL
	}

	return &nieblav1.CreateWorkspaceResponse{
		WorkspaceId:  ws.ID,
		BaseCl:       baseCL,
		RootTreeHash: rootTreeHash,
	}, nil
}

// ShelveCL creates a new changelist or adds an updated patchset to an existing changelist.
func (s *Server) ShelveCL(ctx context.Context, req *nieblav1.ShelveCLRequest) (*nieblav1.ShelveCLResponse, error) {
	if req.Author == "" {
		return nil, status.Error(codes.InvalidArgument, "author cannot be empty")
	}
	if len(req.RootTreeHash) == 0 {
		return nil, status.Error(codes.InvalidArgument, "root_tree_hash cannot be empty")
	}

	clID, oldRootTree, patchsetNumber, parentCLID, description, err := s.recordShelvedPatchset(ctx, req)
	if err != nil {
		return nil, err
	}

	s.assignReviewersAndAgents(ctx, clID, parentCLID, patchsetNumber, req.RootTreeHash, req.Reviewers)

	// Offload cascading rebase to background queue for sub-100ms shelve response
	if len(oldRootTree) > 0 && !bytes.Equal(oldRootTree, req.RootTreeHash) {
		if s.rebaseQueue != nil {
			s.rebaseQueue.Enqueue(clID, oldRootTree, req.RootTreeHash)
		} else {
			go func() {
				_, _ = s.CascadeStackRebase(context.Background(), clID, oldRootTree, req.RootTreeHash)
			}()
		}
	}

	if s.emitter != nil {
		s.emitter.Emit(ctx, pkgevents.New(
			pkgevents.TypeCLCreated,
			"niebla-server",
			fmt.Sprintf("cl/%d", clID),
			pkgevents.CLCreatedPayload{
				CLID:           fmt.Sprintf("%d", clID),
				Author:         req.Author,
				Description:    description,
				RootTreeHash:   hex.EncodeToString(req.RootTreeHash),
				ParentCLID:     fmt.Sprintf("%d", parentCLID),
				PatchsetNumber: patchsetNumber,
				Reviewers:      req.Reviewers,
			},
		))
	}

	return &nieblav1.ShelveCLResponse{ClId: clID}, nil
}

// SubmitCL atomically transitions a changelist to 'submitted' in a single transaction.
func (s *Server) SubmitCL(ctx context.Context, req *nieblav1.SubmitCLRequest) (*nieblav1.SubmitCLResponse, error) {
	if req.ClId <= 0 {
		return nil, status.Error(codes.InvalidArgument, "valid cl_id is required")
	}

	if !req.Force {
		clInfo, touchedFiles, ownersEval, err := s.validateSubmitPreconditions(ctx, req.ClId)
		if err != nil {
			return nil, err
		}
		if err := s.executePresubmitHooks(ctx, clInfo, touchedFiles, ownersEval); err != nil {
			return nil, err
		}
	}

	var mergeConflictSummary string
	cl, err := s.db.SubmitCL(ctx, req.ClId, func(mCtx context.Context, cl *db.Changelist, headCL *db.Changelist) ([]byte, error) {
		return s.performTrunkMerge(mCtx, cl, headCL, &mergeConflictSummary)
	})
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "changelist %d not found", req.ClId)
		}
		if errors.Is(err, tree.ErrMergeConflict) {
			_ = s.db.MarkCLRebaseConflict(ctx, req.ClId, mergeConflictSummary)
			return nil, status.Errorf(codes.FailedPrecondition,
				"cannot submit CL %d: merge conflict with trunk head (%s). Run 'niebla sync' to resolve",
				req.ClId, mergeConflictSummary)
		}
		return nil, status.Errorf(codes.Internal, "failed to submit changelist: %v", err)
	}

	if s.emitter != nil {
		var trunkRev int64
		if cl.TrunkRevision != nil {
			trunkRev = *cl.TrunkRevision
		}
		s.emitter.Emit(ctx, pkgevents.New(
			pkgevents.TypeCLSubmitted,
			"niebla-server",
			fmt.Sprintf("cl/%d", cl.CLID),
			pkgevents.CLSubmittedPayload{
				CLID:          fmt.Sprintf("%d", cl.CLID),
				Author:        cl.Author,
				TrunkRevision: trunkRev,
				RootTreeHash:  hex.EncodeToString(cl.RootTreeHash),
			},
		))
	}

	return &nieblav1.SubmitCLResponse{
		ClId:            cl.CLID,
		CommittedAtUnix: cl.CommittedAt.Unix(),
	}, nil
}

// GetHeadCL retrieves the current active head submitted changelist.
func (s *Server) GetHeadCL(ctx context.Context, req *nieblav1.GetHeadCLRequest) (*nieblav1.GetHeadCLResponse, error) {
	cl, err := s.db.GetHeadCL(ctx)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "no submitted changelist found")
		}
		return nil, status.Errorf(codes.Internal, "failed to query head CL: %v", err)
	}

	return &nieblav1.GetHeadCLResponse{
		ClId:         cl.CLID,
		RootTreeHash: cl.RootTreeHash,
	}, nil
}

// GetCL retrieves metadata and rebase status for a changelist by ID.
func (s *Server) GetCL(ctx context.Context, req *nieblav1.GetCLRequest) (*nieblav1.GetCLResponse, error) {
	cl, err := s.db.GetCL(ctx, req.ClId)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "changelist %d not found", req.ClId)
		}
		return nil, status.Errorf(codes.Internal, "failed to query CL %d: %v", req.ClId, err)
	}

	parentCL := int64(0)
	if cl.ParentCL != nil {
		parentCL = *cl.ParentCL
	}
	conflictDetails := ""
	if cl.RebaseConflictDetails != nil {
		conflictDetails = *cl.RebaseConflictDetails
	}

	psNum, _ := s.db.GetNextPatchsetNumber(ctx, cl.CLID)
	currentPS := int32(psNum - 1)
	if currentPS < 1 {
		currentPS = 1
	}

	return &nieblav1.GetCLResponse{
		ClId:                  cl.CLID,
		Author:                cl.Author,
		Description:           cl.Description,
		RootTreeHash:          cl.RootTreeHash,
		ParentCl:              parentCL,
		Status:                cl.Status,
		NeedsRebase:           cl.NeedsRebase,
		RebaseConflictDetails: conflictDetails,
		CurrentPatchset:       currentPS,
	}, nil
}

// SyncWorkspace queries changelist and parent Merkle tree status to support client-side sync.
func (s *Server) SyncWorkspace(ctx context.Context, req *nieblav1.SyncWorkspaceRequest) (*nieblav1.SyncWorkspaceResponse, error) {
	var targetCLID int64 = req.ClId
	if targetCLID <= 0 && req.WorkspaceId != "" {
		ws, err := s.db.GetWorkspace(ctx, req.WorkspaceId)
		if err == nil && ws != nil && ws.BaseCL != nil {
			targetCLID = *ws.BaseCL
		}
	}

	if targetCLID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "a valid cl_id or workspace_id is required for sync")
	}

	cl, err := s.db.GetCL(ctx, targetCLID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "changelist %d not found: %v", targetCLID, err)
	}

	var parentCL int64
	var parentRootTree []byte
	if cl.ParentCL != nil && *cl.ParentCL > 0 {
		parentCL = *cl.ParentCL

		// On-Demand Sync-Through: flush pending parent rebase synchronously
		_ = s.SyncThrough(ctx, cl.CLID)
		if refreshed, err := s.db.GetCL(ctx, cl.CLID); err == nil && refreshed != nil {
			cl = refreshed
		}

		parent, err := s.db.GetCL(ctx, parentCL)
		if err == nil && parent != nil {
			parentRootTree = parent.RootTreeHash
		}
	}

	conflictDetails := ""
	if cl.RebaseConflictDetails != nil {
		conflictDetails = *cl.RebaseConflictDetails
	}

	psNum, _ := s.db.GetNextPatchsetNumber(ctx, cl.CLID)
	currentPS := int32(psNum - 1)
	if currentPS < 1 {
		currentPS = 1
	}

	return &nieblav1.SyncWorkspaceResponse{
		WorkspaceId:           req.WorkspaceId,
		ClId:                  cl.CLID,
		ParentCl:              parentCL,
		RootTreeHash:          cl.RootTreeHash,
		ParentRootTreeHash:    parentRootTree,
		NeedsRebase:           cl.NeedsRebase,
		RebaseConflictDetails: conflictDetails,
		PatchsetNumber:        currentPS,
	}, nil
}

// SyncCL is the niebla.v1 method signature for workspace synchronization.
func (s *Server) SyncCL(ctx context.Context, req *nieblav1.SyncCLRequest) (*nieblav1.SyncCLResponse, error) {
	wsResp, err := s.SyncWorkspace(ctx, &nieblav1.SyncWorkspaceRequest{
		WorkspaceId: req.WorkspaceId,
		ClId:        req.ClId,
	})
	if err != nil {
		return nil, err
	}
	return &nieblav1.SyncCLResponse{
		WorkspaceId:           wsResp.WorkspaceId,
		ClId:                  wsResp.ClId,
		ParentCl:              wsResp.ParentCl,
		RootTreeHash:          wsResp.RootTreeHash,
		ParentRootTreeHash:    wsResp.ParentRootTreeHash,
		NeedsRebase:           wsResp.NeedsRebase,
		RebaseConflictDetails: wsResp.RebaseConflictDetails,
		PatchsetNumber:        wsResp.PatchsetNumber,
	}, nil
}

// ReportCheckRun records or updates a check run status for a changelist patchset.
func (s *Server) ReportCheckRun(ctx context.Context, run db.CheckRun) (*db.CheckRun, error) {
	return s.db.UpsertCheckRun(ctx, run)
}

func (s *Server) newOwnersEvaluator(ctx context.Context, rootTreeHash []byte, fetchTree tree.FetchTreeFunc) *owners.Evaluator {
	ownersProvider := func(dir string) ([]byte, error) {
		ownersPath := "OWNERS.toml"
		if dir != "" {
			ownersPath = dir + "/OWNERS.toml"
		}
		entry, err := tree.ResolvePath(rootTreeHash, ownersPath, fetchTree)
		if err != nil || entry == nil || entry.IsDir() || len(entry.BlobSHA256) == 0 {
			return nil, os.ErrNotExist
		}
		f, _, err := s.casStorage.OpenBlob(entry.BlobSHA256)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(f)
	}

	var groupResolver owners.GroupResolver
	if s.toolchainConfig != nil && s.toolchainConfig.Critique.Groups.Mode == "static" {
		groupResolver = func(group string) []string {
			groupKey := strings.TrimPrefix(group, "@")
			return s.toolchainConfig.Critique.Groups.Static[groupKey]
		}
	}

	return owners.NewEvaluator(ownersProvider, groupResolver)
}

func (s *Server) dispatchReviewAgents(clID int64, patchset int, rootTreeHash []byte, touchedFiles []string, matchedAgents []agents.AgentDef) {
	ctx := context.Background()
	wsDir, cleanup, err := s.materializeWorkspaceForExecution(ctx, rootTreeHash, touchedFiles)
	if err != nil {
		log.Printf("warning: failed to materialize workspace for review agents on CL %d: %v", clID, err)
		return
	}
	defer cleanup()

	var aiCfg *config.CritiqueAIConfig
	if s.toolchainConfig != nil {
		aiCfg = &s.toolchainConfig.Critique.AI
	}

	executor := internalagents.NewExecutor(s.db, s.casStorage, "").WithAIConfig(aiCfg)
	for _, agentDef := range matchedAgents {
		if strings.TrimSpace(agentDef.Type) == "" && strings.TrimSpace(agentDef.Command) == "" {
			continue
		}
		_, err := executor.Execute(ctx, internalagents.ExecutionRequest{
			CLID:         clID,
			Patchset:     patchset,
			Agent:        agentDef,
			WorkspaceDir: wsDir,
			TouchedFiles: touchedFiles,
		})
		if err != nil {
			log.Printf("warning: review agent %s execution error on CL %d: %v", agentDef.Name, clID, err)
		}
	}
}

func (s *Server) materializeWorkspaceForExecution(ctx context.Context, rootTreeHash []byte, touchedFiles []string) (string, func(), error) {
	wsDir, err := os.MkdirTemp("", "niebla-exec-ws-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(wsDir)
	}

	fetchTree := func(treeHash []byte) ([]tree.Entry, error) {
		return s.getTreeEntries(ctx, treeHash)
	}

	allPaths, err := tree.DiffTreeFiles(tree.EmptyTreeHash, rootTreeHash, fetchTree)
	if err != nil {
		return wsDir, cleanup, nil
	}

	touchedSet := make(map[string]bool, len(touchedFiles))
	for _, tf := range touchedFiles {
		touchedSet[tf] = true
	}

	for _, relPath := range allPaths {
		if !touchedSet[relPath] && !strings.HasPrefix(relPath, ".niebla/") && !strings.HasPrefix(relPath, "tools/agents/") {
			continue
		}
		entry, rErr := tree.ResolvePath(rootTreeHash, relPath, fetchTree)
		if rErr != nil || entry == nil || entry.IsDir() || len(entry.BlobSHA256) == 0 {
			continue
		}
		destPath := filepath.Join(wsDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			continue
		}
		f, _, err := s.casStorage.OpenBlob(entry.BlobSHA256)
		if err != nil {
			continue
		}
		data, rErr := io.ReadAll(f)
		f.Close()
		if rErr != nil {
			continue
		}
		perm := os.FileMode(0644)
		if entry.Mode&0111 != 0 || strings.HasPrefix(relPath, "tools/agents/") {
			perm = 0755
		}
		_ = os.WriteFile(destPath, data, perm)
	}

	return wsDir, cleanup, nil
}

func (s *Server) resolveCASBinaryPath(shaHex string) (string, error) {
	shaBytes, err := hex.DecodeString(strings.TrimSpace(shaHex))
	if err != nil || len(shaBytes) != 32 {
		return "", fmt.Errorf("invalid sha256 hex string: %s", shaHex)
	}
	home, _ := os.UserHomeDir()
	toolsDir := filepath.Join(home, ".cache", "niebla", "tools")
	_ = os.MkdirAll(toolsDir, 0755)
	destPath := filepath.Join(toolsDir, shaHex)
	if _, err := os.Stat(destPath); err == nil {
		return destPath, nil
	}

	f, _, err := s.casStorage.OpenBlob(shaBytes)
	if err != nil {
		return "", fmt.Errorf("blob %s not found in CAS storage: %w", shaHex, err)
	}
	defer f.Close()

	tmpPath := destPath + ".tmp"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, f); err != nil {
		out.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	out.Close()
	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", err
	}
	return destPath, nil
}
