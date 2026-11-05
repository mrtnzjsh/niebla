package grpc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"niebla/internal/db"
	"niebla/pkg/agents"
	"niebla/pkg/hooks"
	"niebla/pkg/owners"
	nieblav1 "niebla/pkg/proto/nieblav1"
	"niebla/pkg/tree"
	"niebla/pkg/types"
)

// SubmitContext encapsulates the evaluated state required to commit a CL and run presubmits.
type SubmitContext struct {
	CL           *db.Changelist
	TouchedFiles []string
	OwnersEval   *owners.EvaluationResult
}

// validateSubmitPreconditions verifies that a CL is ready to be committed to trunk.
func (s *Server) validateSubmitPreconditions(ctx context.Context, clID int64) (*SubmitContext, error) {
	clInfo, err := s.db.GetCL(ctx, clID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "changelist %d not found: %v", clID, err)
	}
	if clInfo.NeedsRebase {
		return nil, status.Errorf(codes.FailedPrecondition,
			"cannot submit CL %d: changelist has unresolved rebase conflicts with parent CL. Run 'niebla sync' in workspace to resolve",
			clID)
	}

	baseTree := tree.EmptyTreeHash
	if clInfo.ParentCL != nil && *clInfo.ParentCL > 0 {
		if parentCL, err := s.db.GetCL(ctx, *clInfo.ParentCL); err == nil && parentCL != nil {
			baseTree = parentCL.RootTreeHash
		}
	}

	fetchTree := func(treeHash []byte) ([]tree.Entry, error) {
		return s.getTreeEntries(ctx, treeHash)
	}

	touchedFiles, err := tree.DiffTreeFiles(baseTree, clInfo.RootTreeHash, fetchTree)
	if err != nil {
		log.Printf("warning: failed to diff tree files for CL %d: %v", clID, err)
	}

	var ownersEval *owners.EvaluationResult
	if len(touchedFiles) > 0 {
		evaluator := s.newOwnersEvaluator(ctx, clInfo.RootTreeHash, fetchTree)
		approvedReviewers, _ := s.db.GetApprovedReviewers(ctx, clID)
		evalRes, evalErr := evaluator.Evaluate(touchedFiles, approvedReviewers)
		if evalErr != nil {
			log.Printf("warning: failed to evaluate OWNERS for CL %d: %v", clID, evalErr)
		} else {
			ownersEval = evalRes
		}
	}

	if err := s.checkUnresolvedComments(ctx, clID, ownersEval); err != nil {
		return nil, err
	}
	if err := s.checkRequiredApprovals(ctx, clID, ownersEval); err != nil {
		return nil, err
	}
	if err := s.checkGreenCI(ctx, clID, ownersEval); err != nil {
		return nil, err
	}
	if err := s.checkOwnersSatisfaction(clID, ownersEval); err != nil {
		return nil, err
	}

	return &SubmitContext{
		CL:           clInfo,
		TouchedFiles: touchedFiles,
		OwnersEval:   ownersEval,
	}, nil
}

func (s *Server) checkUnresolvedComments(ctx context.Context, clID int64, ownersEval *owners.EvaluationResult) error {
	unresolved, err := s.db.CountUnresolvedComments(ctx, clID)
	if err != nil {
		log.Printf("warning: failed to count unresolved comments for CL %d: %v", clID, err)
		return nil
	}
	if unresolved <= 0 {
		return nil
	}
	allowUnresolved := false
	if s.toolchainConfig != nil {
		allowUnresolved = s.toolchainConfig.Critique.GetAllowSubmitWithUnresolvedComments()
	}
	if ownersEval != nil && ownersEval.Policy.AllowUnresolvedComments != nil {
		if !*ownersEval.Policy.AllowUnresolvedComments || s.toolchainConfig == nil {
			allowUnresolved = *ownersEval.Policy.AllowUnresolvedComments
		}
	}
	if !allowUnresolved {
		return status.Errorf(codes.FailedPrecondition,
			"cannot submit CL %d: %d unresolved comment thread(s) pending. Resolve all comments before submitting to trunk",
			clID, unresolved)
	}
	return nil
}

func (s *Server) checkRequiredApprovals(ctx context.Context, clID int64, ownersEval *owners.EvaluationResult) error {
	requiredApprovals := 0
	if s.toolchainConfig != nil {
		requiredApprovals = s.toolchainConfig.Critique.GetRequiredApprovals()
	}
	if ownersEval != nil && ownersEval.Policy.RequiredApprovals != nil {
		if *ownersEval.Policy.RequiredApprovals > requiredApprovals {
			requiredApprovals = *ownersEval.Policy.RequiredApprovals
		}
	}
	if requiredApprovals <= 0 {
		return nil
	}
	approvals, err := s.db.CountApprovals(ctx, clID)
	if err != nil {
		log.Printf("warning: failed to count approvals for CL %d: %v", clID, err)
		return nil
	}
	if approvals < requiredApprovals {
		return status.Errorf(codes.FailedPrecondition,
			"cannot submit CL %d: requires %d approval(s), but only %d recorded",
			clID, requiredApprovals, approvals)
	}
	return nil
}

func (s *Server) checkGreenCI(ctx context.Context, clID int64, ownersEval *owners.EvaluationResult) error {
	requireGreen := false
	if s.toolchainConfig != nil {
		requireGreen = s.toolchainConfig.Critique.GetRequireGreenCheckRuns()
	}
	if ownersEval != nil && ownersEval.Policy.RequireGreenCI != nil && *ownersEval.Policy.RequireGreenCI {
		requireGreen = true
	}
	if !requireGreen {
		return nil
	}

	latestPS, err := s.db.GetLatestPatchsetNumber(ctx, clID)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to get latest patchset for CL %d: %v", clID, err)
	}
	ciSummary, err := s.db.AreCheckRunsGreen(ctx, clID, latestPS)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to check CI check runs: %v", err)
	}
	if !ciSummary.Green {
		if ciSummary.Total == 0 {
			return status.Errorf(codes.FailedPrecondition,
				"cannot submit CL %d: no CI check runs found for patchset %d (policy requires green check runs)",
				clID, latestPS)
		}
		return status.Errorf(codes.FailedPrecondition,
			"cannot submit CL %d: CI check runs not passing for patchset %d (%d pending/running, %d failed out of %d total)",
			clID, latestPS, ciSummary.Pending, ciSummary.Failed, ciSummary.Total)
	}

	if ownersEval != nil && len(ownersEval.Policy.CIRunners) > 0 {
		return s.checkRequiredRunners(ctx, clID, latestPS, ownersEval.Policy.CIRunners)
	}
	return nil
}

func (s *Server) checkRequiredRunners(ctx context.Context, clID int64, latestPS int, requiredRunners []string) error {
	runs, rErr := s.db.ListCheckRunsForPatchset(ctx, clID, latestPS)
	if rErr != nil {
		return nil
	}
	missingRunners := db.FindMissingRequiredRunners(runs, requiredRunners)
	if len(missingRunners) > 0 {
		return status.Errorf(codes.FailedPrecondition,
			"cannot submit CL %d: required OWNERS.toml CI runner(s) not passing for patchset %d: %s",
			clID, latestPS, strings.Join(missingRunners, ", "))
	}
	return nil
}

func (s *Server) checkOwnersSatisfaction(clID int64, ownersEval *owners.EvaluationResult) error {
	if ownersEval == nil || ownersEval.Satisfied {
		return nil
	}
	var missingDesc []string
	for _, missing := range ownersEval.Missing {
		missingDesc = append(missingDesc, fmt.Sprintf("%s (%s owners: %s)", missing.Path, missing.Directory, strings.Join(missing.Approvers, ", ")))
	}
	return status.Errorf(codes.FailedPrecondition,
		"cannot submit CL %d: missing required OWNERS approval for: %s",
		clID, strings.Join(missingDesc, "; "))
}

func buildTouchedFiles(clInfo *db.Changelist, touchedFiles []string, fetchTree tree.FetchTreeFunc) []hooks.TouchedFile {
	var touched []hooks.TouchedFile
	for _, path := range touchedFiles {
		entry, _ := tree.ResolvePath(clInfo.RootTreeHash, path, fetchTree)
		changeType := types.ChangeTypeModified
		var size uint64
		var blobSHA []byte
		if entry == nil {
			changeType = types.ChangeTypeDeleted
		} else {
			size = entry.Size
			blobSHA = entry.BlobSHA256
		}
		touched = append(touched, hooks.TouchedFile{
			Path:      path,
			SizeBytes: size,
			BlobSHA:   blobSHA,
			Status:    string(changeType),
		})
	}
	return touched
}

func checkPresubmitFailures(evalRes hooks.EvaluationResult, clID int64) error {
	if evalRes.Passed {
		return nil
	}
	var failureMsgs []string
	for _, fail := range evalRes.Failures {
		if fail.Blocking {
			if fail.Path != "" {
				failureMsgs = append(failureMsgs, fmt.Sprintf("[%s] %s: %s", fail.HookName, fail.Path, fail.Message))
			} else {
				failureMsgs = append(failureMsgs, fmt.Sprintf("[%s] %s", fail.HookName, fail.Message))
			}
		}
	}
	if len(failureMsgs) == 0 {
		return nil
	}
	return status.Errorf(codes.FailedPrecondition,
		"cannot submit CL %d: presubmit hook(s) failed: %s",
		clID, strings.Join(failureMsgs, "; "))
}

// executePresubmitHooks evaluates .niebla/hooks.toml server presubmits.
func (s *Server) executePresubmitHooks(ctx context.Context, sCtx *SubmitContext) error {
	if sCtx == nil || sCtx.CL == nil {
		return nil
	}
	clInfo := sCtx.CL
	touchedFiles := sCtx.TouchedFiles
	ownersEval := sCtx.OwnersEval
	fetchTree := func(th []byte) ([]tree.Entry, error) {
		return s.getTreeEntries(ctx, th)
	}
	hooksEntry, err := tree.ResolvePath(clInfo.RootTreeHash, ".niebla/hooks.toml", fetchTree)
	if err != nil || hooksEntry == nil || hooksEntry.IsDir() || len(hooksEntry.BlobSHA256) == 0 {
		return nil
	}

	f, _, err := s.casStorage.OpenBlob(hooksEntry.BlobSHA256)
	if err != nil {
		log.Printf("warning: failed to open .niebla/hooks.toml blob for CL %d: %v", clInfo.CLID, err)
		return nil
	}
	defer f.Close()

	hooksData, rErr := io.ReadAll(f)
	if rErr != nil {
		log.Printf("warning: failed to read .niebla/hooks.toml data for CL %d: %v", clInfo.CLID, rErr)
		return nil
	}

	hooksCfg, pErr := hooks.ParseTOML(hooksData)
	if pErr != nil || hooksCfg == nil || len(hooksCfg.Server.Presubmit) == 0 {
		return nil
	}

	var parentCLID int64
	if clInfo.ParentCL != nil {
		parentCLID = *clInfo.ParentCL
	}
	clMeta := hooks.CLMetadata{
		CLID:        clInfo.CLID,
		Author:      clInfo.Author,
		Description: clInfo.Description,
		ParentCLID:  parentCLID,
	}

	touched := buildTouchedFiles(clInfo, touchedFiles, fetchTree)

	var explicitHooks []string
	if ownersEval != nil {
		explicitHooks = ownersEval.Policy.PresubmitHooks
	}
	var wsDir string
	var cleanupWS func()
	for _, hk := range hooksCfg.Server.Presubmit {
		if hk.Type == hooks.HookTypeInTree || hk.Type == hooks.HookTypeCASBinary || hk.Type == hooks.HookTypeExec {
			wsDir, cleanupWS, _ = s.materializeWorkspaceForExecution(ctx, clInfo.RootTreeHash, touchedFiles)
			break
		}
	}
	if cleanupWS != nil {
		defer cleanupWS()
	}

	evalRes := hooks.EvaluateServerPresubmitWithOptions(hooksCfg.Server.Presubmit, clMeta, touched, hooks.ServerPresubmitOptions{
		ExplicitHooks:     explicitHooks,
		WorkspaceDir:      wsDir,
		CASBinaryResolver: s.resolveCASBinaryPath,
	})
	return checkPresubmitFailures(evalRes, clInfo.CLID)
}

// performTrunkMerge executes the 3-way tree merge against trunk HEAD.
func (s *Server) performTrunkMerge(mCtx context.Context, cl *db.Changelist, headCL *db.Changelist, conflictSummary *string) ([]byte, error) {
	var ancestorTreeHash []byte = tree.EmptyTreeHash
	if cl.ParentCL != nil && *cl.ParentCL > 0 {
		if *cl.ParentCL == headCL.CLID {
			// Fast-path: parent is already current trunk head
			return cl.RootTreeHash, nil
		}
		if parent, pErr := s.db.GetCL(mCtx, *cl.ParentCL); pErr == nil && parent != nil {
			ancestorTreeHash = parent.RootTreeHash
		}
	}

	if bytes.Equal(ancestorTreeHash, headCL.RootTreeHash) {
		return cl.RootTreeHash, nil
	}

	fetchTree := func(treeHash []byte) ([]tree.Entry, error) {
		return s.getTreeEntries(mCtx, treeHash)
	}
	readBlob := func(sha []byte) ([]byte, error) {
		f, _, err := s.casStorage.OpenBlob(sha)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(f)
	}
	writeBlob := func(data []byte) ([]byte, error) {
		blobInfo, err := s.casStorage.Put(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		_ = s.db.InsertBlob(mCtx, blobInfo.HashBytes, blobInfo.Size, blobInfo.StoragePath)
		return blobInfo.HashBytes, nil
	}
	writeTree := func(entries []tree.Entry) ([]byte, error) {
		treeHash := tree.ComputeTreeHash(entries)
		if err := s.db.InsertTreeNodes(mCtx, treeHash, entries); err != nil {
			return nil, err
		}
		return treeHash, nil
	}

	mergedHash, conflicts, mergeErr := tree.ThreeWayTreeMerge(
		mCtx,
		ancestorTreeHash,
		headCL.RootTreeHash,
		cl.RootTreeHash,
		fetchTree,
		readBlob,
		writeBlob,
		writeTree,
		fmt.Sprintf("TRUNK HEAD CL %d", headCL.CLID),
		fmt.Sprintf("CL %d", cl.CLID),
	)
	if mergeErr != nil || len(conflicts) > 0 {
		var sb strings.Builder
		for _, conflict := range conflicts {
			if conflict.ConflictHunks > 0 {
				sb.WriteString(fmt.Sprintf("%s (%d conflict hunks); ", conflict.Path, conflict.ConflictHunks))
			} else {
				sb.WriteString(fmt.Sprintf("%s (%s); ", conflict.Path, conflict.Reason))
			}
		}
		summary := strings.TrimSuffix(sb.String(), "; ")
		if summary == "" && mergeErr != nil {
			summary = mergeErr.Error()
		}
		*conflictSummary = summary
		return nil, tree.ErrMergeConflict
	}
	return mergedHash, nil
}

// ShelvedPatchsetResult contains the identifiers and metadata of a recorded or updated patchset.
type ShelvedPatchsetResult struct {
	CLID           int64
	OldRootTree    []byte
	PatchsetNumber int
	ParentCLID     int64
	Description    string
}

// recordShelvedPatchset creates or updates the shelved changelist record.
func (s *Server) recordShelvedPatchset(ctx context.Context, req *nieblav1.ShelveCLRequest) (*ShelvedPatchsetResult, error) {
	patchsetNumber := 1
	parentCLID := req.ParentCl
	description := req.Description
	var oldRootTree []byte
	clID := req.ClId

	if req.ClId > 0 {
		existingCL, getErr := s.db.GetCL(ctx, req.ClId)
		if getErr != nil {
			return nil, status.Errorf(codes.NotFound, "changelist %d not found: %v", req.ClId, getErr)
		}
		clID = req.ClId
		oldRootTree = existingCL.RootTreeHash
		if existingCL.ParentCL != nil {
			parentCLID = *existingCL.ParentCL
		}
		if description == "" {
			description = existingCL.Description
		}

		nextPS, psErr := s.db.GetNextPatchsetNumber(ctx, clID)
		if psErr != nil {
			return nil, status.Errorf(codes.Internal, "failed to get next patchset number: %v", psErr)
		}
		patchsetNumber = nextPS

		if err := s.db.CreatePatchset(ctx, clID, nextPS, req.RootTreeHash); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to create patchset %d: %v", nextPS, err)
		}
		if err := s.db.UpdateCLRootTreeHash(ctx, clID, req.RootTreeHash); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to update root tree hash: %v", err)
		}
		_ = s.db.ClearCLRebaseConflict(ctx, clID)
		return &ShelvedPatchsetResult{
			CLID:           clID,
			OldRootTree:    oldRootTree,
			PatchsetNumber: patchsetNumber,
			ParentCLID:     parentCLID,
			Description:    description,
		}, nil
	}

	newID, createErr := s.db.CreateShelvedCL(ctx, req.Author, req.Description, req.RootTreeHash, req.ParentCl)
	if createErr != nil {
		return nil, status.Errorf(codes.Internal, "failed to shelve CL: %v", createErr)
	}
	_ = s.db.CreatePatchset(ctx, newID, 1, req.RootTreeHash)
	return &ShelvedPatchsetResult{
		CLID:           newID,
		OldRootTree:    nil,
		PatchsetNumber: 1,
		ParentCLID:     parentCLID,
		Description:    description,
	}, nil
}

// assignReviewersAndAgents auto-assigns human reviewers and AI review agents based on OWNERS.toml and agents.toml.
func (s *Server) assignReviewersAndAgents(ctx context.Context, clID int64, parentCLID int64, patchsetNumber int, rootTreeHash []byte, explicitReviewers []string) {
	if len(explicitReviewers) > 0 {
		if err := s.db.AddReviewers(ctx, clID, explicitReviewers); err != nil {
			log.Printf("warning: failed to assign reviewers to CL %d: %v", clID, err)
		}
	}

	fetchTree := func(treeHash []byte) ([]tree.Entry, error) {
		return s.getTreeEntries(ctx, treeHash)
	}
	var baseTree []byte = tree.EmptyTreeHash
	if parentCLID > 0 {
		if parentCL, err := s.db.GetCL(ctx, parentCLID); err == nil && parentCL != nil {
			baseTree = parentCL.RootTreeHash
		}
	}
	touchedFiles, _ := tree.DiffTreeFiles(baseTree, rootTreeHash, fetchTree)

	var explicitAgents []string
	if len(touchedFiles) == 0 {
		s.dispatchAgentPersonas(ctx, clID, patchsetNumber, rootTreeHash, touchedFiles, explicitAgents, fetchTree)
		return
	}

	ownersEval := s.newOwnersEvaluator(ctx, rootTreeHash, fetchTree)
	policy, pErr := ownersEval.ResolvePolicy(touchedFiles)
	if pErr == nil && policy != nil {
		explicitAgents = policy.Agents
		ownersReviewers := append([]string{}, policy.Reviewers...)
		ownersReviewers = append(ownersReviewers, policy.Agents...)
		if len(ownersReviewers) > 0 {
			if err := s.db.AddReviewers(ctx, clID, ownersReviewers); err != nil {
				log.Printf("warning: failed to auto-assign OWNERS reviewers/agents to CL %d: %v", clID, err)
			}
		}
	}

	s.dispatchAgentPersonas(ctx, clID, patchsetNumber, rootTreeHash, touchedFiles, explicitAgents, fetchTree)
}

func (s *Server) dispatchAgentPersonas(ctx context.Context, clID int64, patchsetNumber int, rootTreeHash []byte, touchedFiles, explicitAgents []string, fetchTree tree.FetchTreeFunc) {
	agentsEntry, err := tree.ResolvePath(rootTreeHash, ".niebla/agents.toml", fetchTree)
	if err != nil || agentsEntry == nil || agentsEntry.IsDir() || len(agentsEntry.BlobSHA256) == 0 {
		return
	}
	f, _, err := s.casStorage.OpenBlob(agentsEntry.BlobSHA256)
	if err != nil {
		return
	}
	defer f.Close()

	agentsData, rErr := io.ReadAll(f)
	if rErr != nil {
		return
	}
	agentsCfg, pErr := agents.ParseTOML(agentsData)
	if pErr != nil || agentsCfg == nil || len(agentsCfg.Agents) == 0 {
		return
	}

	matched := agents.MatchAgentsWithExplicit(agentsCfg.Agents, touchedFiles, explicitAgents)
	var agentNames []string
	for _, agent := range matched {
		agentNames = append(agentNames, agent.Name)
	}
	if len(agentNames) > 0 {
		if err := s.db.AddReviewers(ctx, clID, agentNames); err != nil {
			log.Printf("warning: failed to auto-assign review agents to CL %d: %v", clID, err)
		}
		rootTreeCopy := append([]byte(nil), rootTreeHash...)
		touchedCopy := append([]string(nil), touchedFiles...)
		matchedCopy := append([]agents.AgentDef(nil), matched...)
		go s.dispatchReviewAgents(clID, patchsetNumber, rootTreeCopy, touchedCopy, matchedCopy)
	}
}

// resolveChildOldParentTree finds the parent tree hash at the time a child was branched or synced.
func resolveChildOldParentTree(ctx context.Context, database *db.DB, child, parent *db.Changelist, parentCLID int64) []byte {
	var oldParentTree []byte
	if child.BasePatchsetNumber > 0 {
		if pps, err := database.GetPatchset(ctx, parentCLID, child.BasePatchsetNumber); err == nil && pps != nil {
			oldParentTree = pps.RootTreeHash
		}
	}
	if len(oldParentTree) == 0 {
		if pps, err := database.GetPatchset(ctx, parentCLID, 1); err == nil && pps != nil {
			oldParentTree = pps.RootTreeHash
		} else {
			oldParentTree = parent.RootTreeHash
		}
	}
	return oldParentTree
}

const (
	// DefaultTreePageSize is the default number of entries returned per GetTree page.
	DefaultTreePageSize = 5000

	// MaxTreePageSize is the maximum allowed page size for GetTree.
	MaxTreePageSize = 10000
)

// paginateTreeEntries slices the full list of proto entries according to the request's page_size and page_token.
func paginateTreeEntries(entries []*nieblav1.NodeEntry, pageSize int32, pageToken string) (*nieblav1.GetTreeResponse, error) {
	total := len(entries)
	if total == 0 {
		return &nieblav1.GetTreeResponse{Entries: []*nieblav1.NodeEntry{}}, nil
	}

	offset := 0
	if pageToken != "" {
		parsed, err := strconv.Atoi(pageToken)
		if err != nil || parsed < 0 {
			return nil, status.Errorf(codes.InvalidArgument, "invalid page_token %q", pageToken)
		}
		offset = parsed
	}

	if offset >= total {
		return &nieblav1.GetTreeResponse{Entries: []*nieblav1.NodeEntry{}}, nil
	}

	limit := int(pageSize)
	if limit <= 0 {
		if pageToken == "" && total <= DefaultTreePageSize {
			// Backward-compatible path: return entire slice if under default limit and not paginating
			return &nieblav1.GetTreeResponse{Entries: entries}, nil
		}
		limit = DefaultTreePageSize
	} else if limit > MaxTreePageSize {
		limit = MaxTreePageSize
	}

	end := offset + limit
	var nextPageToken string
	if end < total {
		nextPageToken = strconv.Itoa(end)
	} else {
		end = total
	}

	return &nieblav1.GetTreeResponse{
		Entries:       entries[offset:end],
		NextPageToken: nextPageToken,
	}, nil
}
