package handlers

import (
	"context"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"niebla/internal/config"
	"niebla/internal/db"
	"niebla/internal/web/diff"
	"niebla/internal/web/views"
	"niebla/pkg/owners"
	"niebla/pkg/tree"
	"niebla/pkg/types"
)

// parsePatchsetQueryParams extracts ?ps= and ?base_ps= from request with defaults.
func parsePatchsetQueryParams(r *http.Request, defaultCurrentPS int) (currentPS, basePS int) {
	currentPS = defaultCurrentPS
	if psQuery := r.URL.Query().Get("ps"); psQuery != "" {
		if parsed, err := strconv.Atoi(psQuery); err == nil && parsed > 0 {
			currentPS = parsed
		}
	}

	basePS = 0
	if basePSQuery := r.URL.Query().Get("base_ps"); basePSQuery != "" {
		if parsed, err := strconv.Atoi(basePSQuery); err == nil && parsed >= 0 {
			basePS = parsed
		}
	}
	return currentPS, basePS
}

// resolveTreeHashes retrieves the base and modified Merkle root tree hashes for given patchsets.
func (h *CLHandler) resolveTreeHashes(ctx context.Context, cl *db.Changelist, currentPS, basePS int) (baseTreeHash, modTreeHash []byte) {
	if psObj, psErr := h.db.GetPatchset(ctx, cl.CLID, currentPS); psErr == nil && len(psObj.RootTreeHash) > 0 {
		modTreeHash = psObj.RootTreeHash
	} else {
		modTreeHash = cl.RootTreeHash
	}

	if basePS == 0 {
		if cl.ParentCL != nil && *cl.ParentCL > 0 {
			if parentCL, parentErr := h.db.GetCL(ctx, *cl.ParentCL); parentErr == nil && len(parentCL.RootTreeHash) > 0 {
				baseTreeHash = parentCL.RootTreeHash
			}
		}
		if len(baseTreeHash) == 0 {
			baseTreeHash = tree.EmptyTreeHash
		}
	} else {
		if basePSObj, basePSErr := h.db.GetPatchset(ctx, cl.CLID, basePS); basePSErr == nil && len(basePSObj.RootTreeHash) > 0 {
			baseTreeHash = basePSObj.RootTreeHash
		} else {
			baseTreeHash = tree.EmptyTreeHash
		}
	}
	return baseTreeHash, modTreeHash
}

// resolveFileDiffDetail computes the side-by-side diff for targetFile between two trees.
func (h *CLHandler) resolveFileDiffDetail(ctx context.Context, targetFile string, baseTreeHash, modTreeHash []byte) (*diff.FileDiffDetail, error) {
	fetchTree := func(hash []byte) ([]tree.Entry, error) {
		return h.db.GetTreeEntries(ctx, hash)
	}

	baseEntry, _ := tree.ResolvePath(baseTreeHash, targetFile, fetchTree)
	modEntry, _ := tree.ResolvePath(modTreeHash, targetFile, fetchTree)

	changeType := "M"
	var baseBlobSHA, modBlobSHA []byte
	switch {
	case baseEntry == nil && modEntry != nil:
		changeType = "A"
		modBlobSHA = modEntry.BlobSHA256
	case baseEntry != nil && modEntry == nil:
		changeType = "D"
		baseBlobSHA = baseEntry.BlobSHA256
	case baseEntry != nil && modEntry != nil:
		baseBlobSHA = baseEntry.BlobSHA256
		modBlobSHA = modEntry.BlobSHA256
	}

	baseData, _ := h.readBlobBytes(baseBlobSHA)
	modData, _ := h.readBlobBytes(modBlobSHA)

	detail, err := diff.ComputeSideBySideDiff(targetFile, baseData, modData, changeType)
	if err == nil && detail != nil {
		if len(baseBlobSHA) > 0 {
			detail.BaseBlobHex = hex.EncodeToString(baseBlobSHA)
		}
		if len(modBlobSHA) > 0 {
			detail.ModBlobHex = hex.EncodeToString(modBlobSHA)
		}
	}
	return detail, err
}

// getFileCommentsByLine retrieves published comments and author's drafts anchored to lines.
func (h *CLHandler) getFileCommentsByLine(ctx context.Context, clID int64, targetFile, reviewer string) map[int][]db.Comment {
	commentsByLine := make(map[int][]db.Comment)
	if targetFile == "" || h.db == nil {
		return commentsByLine
	}
	fileComments, err := h.db.ListComments(ctx, clID, targetFile)
	if err == nil {
		for _, comment := range fileComments {
			if !comment.IsDraft || comment.Author == reviewer {
				anchorLine := comment.EndLineNumber
				if anchorLine <= 0 {
					anchorLine = comment.LineNumber
				}
				commentsByLine[anchorLine] = append(commentsByLine[anchorLine], comment)
			}
		}
	}
	return commentsByLine
}

// populateFileDiffStats computes diff stats and reviewed flags for changed files.
func (h *CLHandler) populateFileDiffStats(files []diff.FileDiffSummary, commentsPerFile map[string]int, reviewedMap map[string]bool) int {
	reviewedCount := 0
	for i := range files {
		files[i].UnresolvedComments = commentsPerFile[files[i].Path]
		files[i].Reviewed = reviewedMap[files[i].Path]
		if files[i].Reviewed {
			reviewedCount++
		}
		baseData, _ := h.readBlobBytes(files[i].BaseBlobSHA)
		modData, _ := h.readBlobBytes(files[i].ModifiedBlobSHA)
		d, err := diff.ComputeSideBySideDiff(files[i].Path, baseData, modData, files[i].Type)
		if err == nil && d != nil {
			files[i].Additions = d.Additions
			files[i].Deletions = d.Deletions
		}
	}
	return reviewedCount
}

// fetchIndexData queries changelists and assigned CL IDs for current user.
func (h *CLHandler) fetchIndexData(ctx context.Context, currentUser string) ([]db.Changelist, map[int64]bool) {
	assignedCLIDs := make(map[int64]bool)
	if h.db == nil {
		demoReviewersMu.RLock()
		for clID, revs := range demoReviewersStore {
			for _, rev := range revs {
				if strings.EqualFold(rev.Reviewer, currentUser) || strings.EqualFold(rev.Reviewer, "@"+currentUser) {
					assignedCLIDs[clID] = true
				}
			}
		}
		demoReviewersMu.RUnlock()
		if len(assignedCLIDs) == 0 {
			assignedCLIDs[2] = true
		}
		return []db.Changelist{
			{
				CLID:        1,
				Author:      "alice@corp",
				Description: "feat(critique): implement fast server-authoritative code review web UI and Myers diff engine",
				Status:      "shelved",
				CommittedAt: time.Now().Add(-2 * time.Hour),
			},
			{
				CLID:        2,
				Author:      "developer@corp",
				Description: "cas: optimize chunk streaming and LRU tree node caching",
				Status:      "shelved",
				CommittedAt: time.Now().Add(-5 * time.Hour),
			},
			{
				CLID:        3,
				Author:      "agent/linter-bot",
				Description: "fix: apply automated gofmt and lint repairs across repository",
				Status:      string(types.CLStatusShelved),
				CommittedAt: time.Now().Add(-1 * time.Hour),
			},
			{
				CLID:        4,
				Author:      "developer@corp",
				Description: "resilience: add native ZFS snapshot streaming driver",
				Status:      string(types.CLStatusSubmitted),
				CommittedAt: time.Now().Add(-24 * time.Hour),
			},
		}, assignedCLIDs
	}

	cls, err := h.db.ListCLs(ctx, 100)
	if err != nil {
		log.Printf("error listing changelists: %v", err)
	}

	var userGroups []string
	if h.groups != nil {
		groups, _ := h.groups.GetUserGroups(ctx, currentUser)
		userGroups = groups
	}
	if assigned, err := h.db.GetAssignedCLIDsForUser(ctx, currentUser, userGroups); err == nil {
		assignedCLIDs = assigned
	}
	return cls, assignedCLIDs
}

// InboxPartitions groups changelists across review buckets and aggregates badge counts.
type InboxPartitions struct {
	MyReviews []views.CLRowData
	Outgoing  []views.CLRowData
	Agents    []views.CLRowData
	Submitted []views.CLRowData
	All       []views.CLRowData
	Counts    views.InboxCounts
}

// SubmitPolicy contains criteria that must be satisfied before a CL can be submitted.
type SubmitPolicy struct {
	ReqApprovals      int
	AllowUnresolved   bool
	RequireGreen      bool
	RequiredCIRunners []string
}

// SubmitCLMetadata bundles CL status, approval counts, and submission policies.
type SubmitCLMetadata struct {
	CLStatus        string
	NeedsRebase     bool
	ApprovalsCount  int
	UnresolvedCount int
	Policy          SubmitPolicy
}

// ActionsBarData loads stats, reviewers, and policy gates required for rendering the actions bar.
type ActionsBarData struct {
	CLStatus        string
	Approvals       []db.Approval
	Reviewers       []db.ReviewerAssignment
	ApprovedCount   int
	UnresolvedCount int
	DraftCount      int
	ReqApprovals    int
	AllowUnresolved bool
}

// classifyInboxRows categorizes changelists into partitioned inbox queues.
func (h *CLHandler) classifyInboxRows(allCLs []db.Changelist, currentUser string, assignedCLIDs map[int64]bool) InboxPartitions {
	var p InboxPartitions
	p.Counts.All = len(allCLs)
	isAgentAuthor := func(author string) bool {
		if h.config != nil {
			return h.config.Critique.IsAgentAuthor(author)
		}
		return config.MatchAgentAuthor(author, config.DefaultAgentAuthorPatterns)
	}

	for _, cl := range allCLs {
		isAgent := isAgentAuthor(cl.Author)
		cleanAuthor := strings.TrimSpace(cl.Author)
		isAuthor := strings.EqualFold(cleanAuthor, currentUser) ||
			strings.HasPrefix(strings.ToLower(cleanAuthor), strings.ToLower(currentUser)+"@")
		isAssigned := assignedCLIDs[cl.CLID]

		row := views.CLRowData{
			Changelist: cl,
			IsAgent:    isAgent,
			IsReviewer: isAssigned,
		}
		p.All = append(p.All, row)

		switch {
		case types.CLStatus(cl.Status).IsSubmitted():
			p.Counts.Submitted++
			p.Submitted = append(p.Submitted, row)
		case isAgent:
			p.Counts.Agents++
			p.Agents = append(p.Agents, row)
		case isAuthor:
			p.Counts.Outgoing++
			p.Outgoing = append(p.Outgoing, row)
		case isAssigned:
			p.Counts.MyReviews++
			p.MyReviews = append(p.MyReviews, row)
		}
	}
	return p
}

// resolveActiveTab determines which tab to display based on parameter and counts.
func resolveActiveTab(tab string, counts views.InboxCounts) string {
	if tab != "" {
		return tab
	}
	switch {
	case counts.MyReviews > 0:
		return "my-reviews"
	case counts.Outgoing > 0:
		return "outgoing"
	case counts.Agents > 0:
		return "agents"
	default:
		return "all"
	}
}

// selectActiveRows returns the slice of rows corresponding to the active tab.
func selectActiveRows(tab string, p InboxPartitions) []views.CLRowData {
	switch tab {
	case "my-reviews":
		return p.MyReviews
	case "outgoing":
		return p.Outgoing
	case "agents":
		return p.Agents
	case string(types.CLStatusSubmitted):
		return p.Submitted
	default:
		return p.All
	}
}

// getSubmitCLMetadata retrieves CL status, approvals count, and policies for submission.
func (h *CLHandler) getSubmitCLMetadata(ctx context.Context, clID int64) (*SubmitCLMetadata, error) {
	meta := &SubmitCLMetadata{}
	if h.config != nil {
		meta.Policy.ReqApprovals = h.config.Critique.GetRequiredApprovals()
		meta.Policy.AllowUnresolved = h.config.Critique.GetAllowSubmitWithUnresolvedComments()
		meta.Policy.RequireGreen = h.config.Critique.GetRequireGreenCheckRuns()
	}

	if h.db != nil {
		cl, clErr := h.db.GetCL(ctx, clID)
		if clErr != nil || cl == nil {
			return nil, fmt.Errorf("CL %d not found", clID)
		}
		meta.CLStatus = cl.Status
		meta.NeedsRebase = cl.NeedsRebase
		meta.ApprovalsCount, _ = h.db.CountApprovals(ctx, clID)
		meta.UnresolvedCount, _ = h.db.CountUnresolvedComments(ctx, clID)
		meta.Policy = h.resolveCLPolicy(ctx, cl)
		return meta, nil
	}

	demoCLStatusMu.RLock()
	meta.CLStatus = demoCLStatusStore[clID]
	if meta.CLStatus == "" {
		meta.CLStatus = string(types.CLStatusShelved)
	}
	demoCLStatusMu.RUnlock()

	demoApprovalsMu.RLock()
	for _, approval := range demoApprovalsStore[clID] {
		if types.Verdict(approval.Verdict).IsApproval() {
			meta.ApprovalsCount++
		}
	}
	demoApprovalsMu.RUnlock()

	demoCommentsMu.RLock()
	for _, list := range demoCommentsStore {
		for _, comment := range list {
			if comment.CLID == clID && !comment.IsDraft && !comment.Resolved {
				meta.UnresolvedCount++
			}
		}
	}
	demoCommentsMu.RUnlock()

	return meta, nil
}

// validateSubmitBlockers checks for rebase conflicts, shelved status, and unresolved comment blockers.
func (h *CLHandler) validateSubmitBlockers(clID int64, clStatus string, needsRebase, allowUnresolved bool, unresolvedCount int) error {
	if clStatus != "shelved" {
		return fmt.Errorf("CL %d cannot be submitted because its status is %q (expected 'shelved')", clID, clStatus)
	}
	if needsRebase {
		return fmt.Errorf("CL %d cannot be submitted because it has pending rebase conflicts with parent CL; run 'niebla sync' in your workspace to resolve", clID)
	}
	if !allowUnresolved && unresolvedCount > 0 {
		return fmt.Errorf("CL %d has %d unresolved comment thread(s); all comments must be resolved before submission", clID, unresolvedCount)
	}
	return nil
}

// validateSubmitApprovals checks required approvals against count, or records an override.
func (h *CLHandler) validateSubmitApprovals(clID int64, reqApprovals, approvalsCount int, justification, reviewer string) error {
	if reqApprovals <= 0 || approvalsCount >= reqApprovals {
		return nil
	}
	if justification == "" {
		return fmt.Errorf("CL %d requires %d approval(s) before submission (found %d)", clID, reqApprovals, approvalsCount)
	}
	log.Printf("CL %d: approval requirement overridden by %q with justification: %q", clID, reviewer, justification)
	return nil
}

// checkRequiredRunners verifies that all runners required by OWNERS.toml have passed.
func (h *CLHandler) checkRequiredRunners(ctx context.Context, clID int64, psNum int, requiredCIRunners []string) error {
	if h.db == nil || len(requiredCIRunners) == 0 {
		return nil
	}
	runs, rErr := h.db.ListCheckRunsForPatchset(ctx, clID, psNum)
	if rErr != nil {
		return nil
	}
	missing := db.FindMissingRequiredRunners(runs, requiredCIRunners)
	if len(missing) > 0 {
		return fmt.Errorf("CL %d requires OWNERS.toml CI runner(s) to pass before submission: %s", clID, strings.Join(missing, ", "))
	}
	return nil
}

// validateSubmitCI verifies CI check runs pass or records an override.
func (h *CLHandler) validateSubmitCI(ctx context.Context, clID int64, requireGreen bool, requiredCIRunners []string, justification, reviewer string) error {
	if !requireGreen {
		return nil
	}

	var summary db.CheckRunsSummary
	var psNum int = 1

	if h.db != nil {
		if latestPS, err := h.db.GetLatestPatchsetNumber(ctx, clID); err == nil {
			psNum = latestPS
			summary, _ = h.db.AreCheckRunsGreen(ctx, clID, latestPS)
		}
	} else {
		demoCheckRunsMu.RLock()
		runs := demoCheckRunsStore[fmt.Sprintf("%d:%d", clID, 1)]
		demoCheckRunsMu.RUnlock()
		summary = db.EvaluateCheckRuns(runs)
	}

	if !summary.Green {
		if justification == "" {
			if summary.Total == 0 {
				return fmt.Errorf("CL %d requires green CI check runs before submission (found 0 check runs for patchset %d)", clID, psNum)
			}
			return fmt.Errorf("CL %d requires green CI check runs before submission (%d pending, %d failed out of %d total for patchset %d)", clID, summary.Pending, summary.Failed, summary.Total, psNum)
		}
		log.Printf("CL %d: CI green check runs requirement overridden by %q with justification: %q", clID, reviewer, justification)
		return nil
	}

	if justification == "" {
		return h.checkRequiredRunners(ctx, clID, psNum, requiredCIRunners)
	}
	return nil
}

// publishDBReview records approval verdicts, creates review comments, and publishes drafts in DB.
func (h *CLHandler) publishDBReview(ctx context.Context, clID int64, author, overallMsg, verdict string) {
	if _, err := h.db.PublishDraftComments(ctx, clID, author); err != nil {
		log.Printf("error publishing drafts: %v", err)
	}
	if overallMsg != "" {
		_, _ = h.db.CreateComment(ctx, clID, 1, "", 0, 0, string(types.CommentSideModified), author, overallMsg, nil, false)
	}
	if v, ok := types.ParseVerdict(verdict); ok && (v.IsApproval() || v == types.VerdictChangesRequested) {
		_ = h.db.SetApproval(ctx, clID, author, string(v))
	}
}

// publishDemoReview records review state in memory demo stores.
func (h *CLHandler) publishDemoReview(clID int64, author, overallMsg, verdict string) {
	demoCommentsMu.Lock()
	for k, list := range demoCommentsStore {
		for i := range list {
			if list[i].CLID == clID && list[i].Author == author && list[i].IsDraft {
				list[i].IsDraft = false
			}
		}
		demoCommentsStore[k] = list
	}
	if overallMsg != "" {
		nextCommentID++
		comment := db.Comment{
			ID:             nextCommentID,
			CLID:           clID,
			PatchsetNumber: 1,
			FilePath:       "",
			LineNumber:     0,
			EndLineNumber:  0,
			Side:           string(types.CommentSideModified),
			Author:         author,
			Body:           overallMsg,
			Resolved:       false,
			IsDraft:        false,
			CreatedAt:      time.Now(),
		}
		key := fmt.Sprintf("%d::0:modified", clID)
		demoCommentsStore[key] = append(demoCommentsStore[key], comment)
	}
	demoCommentsMu.Unlock()

	if v, ok := types.ParseVerdict(verdict); ok && (v.IsApproval() || v == types.VerdictChangesRequested) {
		demoApprovalsMu.Lock()
		apprs := demoApprovalsStore[clID]
		updated := false
		for i := range apprs {
			if strings.EqualFold(apprs[i].Reviewer, author) {
				apprs[i].Verdict = string(v)
				updated = true
				break
			}
		}
		if !updated {
			apprs = append(apprs, db.Approval{CLID: clID, Reviewer: author, Verdict: string(v), UpdatedAt: time.Now()})
		}
		demoApprovalsStore[clID] = apprs
		demoApprovalsMu.Unlock()
	}
}

// formatCheckRunDuration computes a human-readable duration for a check run.
func formatCheckRunDuration(cr db.CheckRun) string {
	if cr.StartedAt == nil {
		return ""
	}
	if cr.CompletedAt != nil {
		return cr.CompletedAt.Sub(*cr.StartedAt).Round(time.Millisecond * 100).String()
	}
	return time.Since(*cr.StartedAt).Round(time.Second).String()
}

// resolveRunnerProvider determines runner provider, checking toolchain config fallback.
func (h *CLHandler) resolveRunnerProvider(name, rawProvider string) string {
	provider := rawProvider
	if (provider == "" || provider == string(config.CIProviderLocal)) && h.config != nil && h.config.CI.Runners != nil {
		if rCfg, ok := h.config.CI.Runners[name]; ok && rCfg.Provider != "" {
			provider = rCfg.Provider
		}
	}
	if provider == "" {
		provider = string(config.CIProviderLocal)
	}
	return provider
}

// consolidateReviewersWithApprovals merges review assignments with latest recorded verdicts.
func consolidateReviewersWithApprovals(rawReviewers []db.ReviewerAssignment, approvals []db.Approval, clID int64) []db.ReviewerAssignment {
	verdictMap := make(map[string]string, len(approvals))
	for _, approval := range approvals {
		verdictMap[approval.Reviewer] = approval.Verdict
	}
	found := make(map[string]bool, len(rawReviewers)+len(approvals))
	var list []db.ReviewerAssignment
	for _, rev := range rawReviewers {
		if v, ok := verdictMap[rev.Reviewer]; ok {
			rev.Verdict = v
		}
		list = append(list, rev)
		found[rev.Reviewer] = true
	}
	for _, approval := range approvals {
		if !found[approval.Reviewer] {
			list = append(list, db.ReviewerAssignment{
				CLID:      clID,
				Reviewer:  approval.Reviewer,
				Verdict:   approval.Verdict,
				UpdatedAt: approval.UpdatedAt,
			})
		}
	}
	return list
}

// countApprovedVerdicts counts how many approvals have approved or lgtm verdicts.
func countApprovedVerdicts(approvals []db.Approval) int {
	cnt := 0
	for _, approval := range approvals {
		if types.Verdict(approval.Verdict).IsApproval() {
			cnt++
		}
	}
	return cnt
}

// resolveDemoCLComments filters comments for a demo CL, counting unresolved threads and reviewer drafts.
func resolveDemoCLComments(clID int64, targetFile, reviewer string) (map[int][]db.Comment, int, []db.Comment) {
	demoCommentsMu.RLock()
	defer demoCommentsMu.RUnlock()

	commentsByLine := make(map[int][]db.Comment)
	unresolvedCount := 0
	var reviewerDrafts []db.Comment

	for key, list := range demoCommentsStore {
		for _, comment := range list {
			if comment.CLID != clID {
				continue
			}
			if comment.Author == reviewer && comment.IsDraft {
				reviewerDrafts = append(reviewerDrafts, comment)
			}
			if !comment.IsDraft && !comment.Resolved {
				unresolvedCount++
			}
			if targetFile != "" && strings.HasPrefix(key, fmt.Sprintf("%d:%s:", clID, targetFile)) {
				if !comment.IsDraft || comment.Author == reviewer {
					anchor := comment.EndLineNumber
					if anchor <= 0 {
						anchor = comment.LineNumber
					}
					commentsByLine[anchor] = append(commentsByLine[anchor], comment)
				}
			}
		}
	}
	return commentsByLine, unresolvedCount, reviewerDrafts
}

// mergeOwnersPolicy updates approval and CI gates according to an evaluated policy.
func mergeOwnersPolicy(policy *owners.SubtreePolicy, base SubmitPolicy) SubmitPolicy {
	if policy == nil {
		return base
	}
	if policy.RequiredApprovals != nil && *policy.RequiredApprovals > base.ReqApprovals {
		base.ReqApprovals = *policy.RequiredApprovals
	}
	if policy.AllowUnresolvedComments != nil {
		base.AllowUnresolved = *policy.AllowUnresolvedComments
	}
	if policy.RequireGreenCI != nil && *policy.RequireGreenCI {
		base.RequireGreen = true
	}
	if len(policy.CIRunners) > 0 {
		base.RequiredCIRunners = policy.CIRunners
	}
	return base
}

// fetchActionsBarData loads all stats and policies needed to render the actions bar.
func (h *CLHandler) fetchActionsBarData(ctx context.Context, clID int64, author string) ActionsBarData {
	data := ActionsBarData{
		CLStatus: "shelved",
	}
	if h.config != nil {
		data.ReqApprovals = h.config.Critique.GetRequiredApprovals()
		data.AllowUnresolved = h.config.Critique.GetAllowSubmitWithUnresolvedComments()
	}

	if h.db != nil {
		if cl, err := h.db.GetCL(ctx, clID); err == nil && cl != nil {
			data.CLStatus = cl.Status
			policy := h.resolveCLPolicy(ctx, cl)
			data.ReqApprovals = policy.ReqApprovals
			data.AllowUnresolved = policy.AllowUnresolved
		}
		data.Approvals, _ = h.db.ListApprovals(ctx, clID)
		data.Reviewers, _ = h.db.ListReviewers(ctx, clID)
		data.ApprovedCount, _ = h.db.CountApprovals(ctx, clID)
		data.UnresolvedCount, _ = h.db.CountUnresolvedComments(ctx, clID)
		drafts, _ := h.db.ListDraftComments(ctx, clID, author)
		data.DraftCount = len(drafts)
		return data
	}

	demoCLStatusMu.RLock()
	if s, ok := demoCLStatusStore[clID]; ok && s != "" {
		data.CLStatus = s
	}
	demoCLStatusMu.RUnlock()

	demoApprovalsMu.RLock()
	data.Approvals = demoApprovalsStore[clID]
	demoApprovalsMu.RUnlock()
	data.ApprovedCount = countApprovedVerdicts(data.Approvals)

	demoReviewersMu.RLock()
	rawReviewers := demoReviewersStore[clID]
	demoReviewersMu.RUnlock()
	data.Reviewers = consolidateReviewersWithApprovals(rawReviewers, data.Approvals, clID)

	demoCommentsMu.RLock()
	for _, list := range demoCommentsStore {
		for _, comment := range list {
			if comment.CLID == clID {
				if comment.IsDraft && comment.Author == author {
					data.DraftCount++
				}
				if !comment.IsDraft && !comment.Resolved {
					data.UnresolvedCount++
				}
			}
		}
	}
	demoCommentsMu.RUnlock()
	return data
}


