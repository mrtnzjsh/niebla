package handlers

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"niebla/internal/config"
	"niebla/internal/db"
	"niebla/internal/groups"
	"niebla/internal/web/auth"
	"niebla/internal/web/diff"
	"niebla/internal/web/views"
	"niebla/pkg/cas"
	"niebla/pkg/events"
	"niebla/pkg/owners"
	"niebla/pkg/tree"
	"niebla/pkg/types"
)

var (
	demoApprovalsMu      sync.RWMutex
	demoApprovalsStore   = make(map[int64][]db.Approval)
	demoFileReviewsMu    sync.RWMutex
	demoFileReviewsStore = make(map[string]bool)
	demoReviewersMu      sync.RWMutex
	demoReviewersStore   = make(map[int64][]db.ReviewerAssignment)
	demoCLStatusMu       sync.RWMutex
	demoCLStatusStore    = make(map[int64]string)
)

// ResetDemoStoresForTest resets all in-memory demo stores. Used in testing.
func ResetDemoStoresForTest() {
	demoCommentsMu.Lock()
	demoCommentsStore = make(map[string][]db.Comment)
	nextCommentID = 100
	demoCommentsMu.Unlock()

	demoFileReviewsMu.Lock()
	demoFileReviewsStore = make(map[string]bool)
	demoFileReviewsMu.Unlock()

	demoApprovalsMu.Lock()
	demoApprovalsStore = make(map[int64][]db.Approval)
	demoApprovalsMu.Unlock()

	demoReviewersMu.Lock()
	demoReviewersStore = make(map[int64][]db.ReviewerAssignment)
	demoReviewersMu.Unlock()

	demoCLStatusMu.Lock()
	demoCLStatusStore = make(map[int64]string)
	demoCLStatusMu.Unlock()

	demoCheckRunsMu.Lock()
	demoCheckRunsStore = make(map[string][]db.CheckRun)
	demoCheckRunsMu.Unlock()
}

// CLHandler handles changelist reviews, patchset switching, diff views, and reviewer verdicts.
type CLHandler struct {
	db           *db.DB
	cas          *cas.Storage
	authResolver auth.UserIdentityResolver
	config       *config.Config
	groups       groups.Resolver
	emitter      events.Emitter
}

// SetDependencies injects the toolchain config and enterprise user group resolver.
func (h *CLHandler) SetDependencies(cfg *config.Config, gr groups.Resolver) {
	h.config = cfg
	h.groups = gr
}

// SetEmitter injects an event publisher into the CLHandler.
func (h *CLHandler) SetEmitter(e events.Emitter) {
	if e != nil {
		h.emitter = e
	} else {
		h.emitter = events.NewNoopEmitter()
	}
}

// NewCLHandler initializes a new CLHandler with optional custom auth resolver.
func NewCLHandler(database *db.DB, casStorage *cas.Storage, resolvers ...auth.UserIdentityResolver) *CLHandler {
	var resolver auth.UserIdentityResolver
	if len(resolvers) > 0 && resolvers[0] != nil {
		resolver = resolvers[0]
	} else {
		resolver = &auth.LocalResolver{DefaultUser: auth.CurrentOSUser()}
	}
	return &CLHandler{
		db:           database,
		cas:          casStorage,
		authResolver: resolver,
		emitter:      events.NewNoopEmitter(),
	}
}

func (h *CLHandler) clURL(r *http.Request, clID int64) string {
	if r != nil {
		scheme := "http"
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		host := r.Host
		if host != "" {
			return fmt.Sprintf("%s://%s/cl/%d", scheme, host, clID)
		}
	}
	return fmt.Sprintf("/cl/%d", clID)
}

func (h *CLHandler) resolveReviewer(r *http.Request) string {
	if h.authResolver != nil {
		return h.authResolver.ResolveUser(r)
	}
	if r != nil {
		if reviewerParam := r.URL.Query().Get("reviewer"); reviewerParam != "" {
			return strings.TrimSpace(reviewerParam)
		}
	}
	return auth.CurrentOSUser()
}

// HandleIndex lists changelists partitioned into segregated inbox queues.
// GET / and GET /cls
func (h *CLHandler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	currentUser := h.resolveReviewer(r)
	allCLs, assignedCLIDs := h.fetchIndexData(r.Context(), currentUser)
	myReviews, outgoing, agents, submitted, all, counts := h.classifyInboxRows(allCLs, currentUser, assignedCLIDs)

	tab := resolveActiveTab(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("tab"))), counts)
	activeRows := selectActiveRows(tab, myReviews, outgoing, agents, submitted, all)

	inboxData := views.InboxViewData{
		ActiveTab:       tab,
		Counts:          counts,
		Changelists:     activeRows,
		CurrentUserName: currentUser,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Header.Get("HX-Request") == "true" {
		_ = views.InboxContent(inboxData).Render(r.Context(), w)
		return
	}
	_ = views.CLList(inboxData).Render(r.Context(), w)
}

// HandleCLView renders the overview and diff for a changelist.
// GET /cl/{id}
func (h *CLHandler) HandleCLView(w http.ResponseWriter, r *http.Request) {
	clID, err := parseCLID(r)
	if err != nil {
		http.Error(w, "invalid changelist id", http.StatusBadRequest)
		return
	}

	selectedFile := r.URL.Query().Get("file")
	h.renderCLPage(w, r, clID, selectedFile)
}

// HandleCLFileDiff renders the dedicated review page for a specific file path.
// GET /cl/{id}/file/{path...}
func (h *CLHandler) HandleCLFileDiff(w http.ResponseWriter, r *http.Request) {
	clID, err := parseCLID(r)
	if err != nil {
		http.Error(w, "invalid changelist id", http.StatusBadRequest)
		return
	}

	// Extract path after /cl/{id}/file/
	pathPrefix := fmt.Sprintf("/cl/%d/file/", clID)
	selectedFile := strings.TrimPrefix(r.URL.Path, pathPrefix)
	selectedFile = strings.TrimPrefix(selectedFile, "/")

	if h.db != nil {
		h.renderCLFileDiffPage(w, r, clID, selectedFile)
	} else {
		h.renderDemoCLFileDiffPage(w, r, clID, selectedFile)
	}
}

// HandleCLDiffContent renders the fresh inline side-by-side diff for an accordion.
// GET /cl/{id}/diff-content?file={path}&ps={ps}&base_ps={base_ps}
func (h *CLHandler) HandleCLDiffContent(w http.ResponseWriter, r *http.Request) {
	clID, err := parseCLID(r)
	if err != nil {
		http.Error(w, "invalid changelist id", http.StatusBadRequest)
		return
	}

	targetFile := r.URL.Query().Get("file")
	if targetFile == "" {
		http.Error(w, "missing file parameter", http.StatusBadRequest)
		return
	}

	h.renderCLDiffContent(w, r, clID, targetFile)
}

// HandleApproval records a reviewer's approval verdict on a changelist.
// POST /cl/{id}/approval
func (h *CLHandler) HandleApproval(w http.ResponseWriter, r *http.Request) {
	clID, err := parseCLID(r)
	if err != nil {
		http.Error(w, "invalid changelist id", http.StatusBadRequest)
		return
	}

	verdict := r.URL.Query().Get("verdict")
	if verdict == "" {
		verdict = r.FormValue("verdict")
	}
	parsedVerdict, ok := types.ParseVerdict(verdict)
	if !ok || (!parsedVerdict.IsApproval() && parsedVerdict != types.VerdictChangesRequested) {
		http.Error(w, "invalid verdict value (must be approved, lgtm, or changes_requested)", http.StatusBadRequest)
		return
	}
	verdict = string(parsedVerdict)

	reviewer := h.resolveReviewer(r)

	if h.db != nil {
		if err := h.db.SetApproval(r.Context(), clID, reviewer, verdict); err != nil {
			log.Printf("failed to set approval: %v", err)
			http.Error(w, "failed to record approval", http.StatusInternalServerError)
			return
		}
	} else {
		demoApprovalsMu.Lock()
		updated := false
		list := demoApprovalsStore[clID]
		for i := range list {
			if list[i].Reviewer == reviewer {
				list[i].Verdict = verdict
				list[i].UpdatedAt = time.Now()
				updated = true
				break
			}
		}
		if !updated {
			list = append(list, db.Approval{
				CLID:      clID,
				Reviewer:  reviewer,
				Verdict:   verdict,
				UpdatedAt: time.Now(),
			})
		}
		demoApprovalsStore[clID] = list
		demoApprovalsMu.Unlock()
	}

	h.emitter.Emit(r.Context(), events.New(
		events.TypeApprovalUpdated,
		"critique",
		strconv.FormatInt(clID, 10),
		events.ApprovalUpdatedPayload{
			CLID:     strconv.FormatInt(clID, 10),
			Reviewer: reviewer,
			Status:   verdict,
			URL:      h.clURL(r, clID),
		},
	))

	h.renderActionsBar(w, r, clID)
}

// HandleAddReviewers assigns one or more reviewers (or expanded groups) to a CL.
// POST /cl/{id}/reviewers
func (h *CLHandler) HandleAddReviewers(w http.ResponseWriter, r *http.Request) {
	clID, err := parseCLID(r)
	if err != nil {
		http.Error(w, "invalid changelist id", http.StatusBadRequest)
		return
	}

	input := r.FormValue("reviewer")
	if input == "" {
		input = r.URL.Query().Get("reviewer")
	}
	input = strings.TrimSpace(input)
	if input == "" {
		h.renderActionsBar(w, r, clID)
		return
	}

	resolver := h.groups
	if resolver == nil {
		resolver = groups.NewResolver(config.CritiqueGroupConfig{Mode: "disabled"})
	}

	expanded, err := groups.ExpandReviewers(r.Context(), resolver, []string{input})
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to expand reviewers: %v", err), http.StatusBadRequest)
		return
	}

	if len(expanded) > 0 {
		if h.db != nil {
			if err := h.db.AddReviewers(r.Context(), clID, expanded); err != nil {
				log.Printf("failed to add reviewers: %v", err)
				http.Error(w, "failed to add reviewers", http.StatusInternalServerError)
				return
			}
		} else {
			demoReviewersMu.Lock()
			current := demoReviewersStore[clID]
			exists := make(map[string]bool)
			for _, rev := range current {
				exists[rev.Reviewer] = true
			}
			for _, username := range expanded {
				if !exists[username] {
					current = append(current, db.ReviewerAssignment{
						CLID:      clID,
						Reviewer:  username,
						Verdict:   "pending",
						UpdatedAt: time.Now(),
					})
					exists[username] = true
				}
			}
			demoReviewersStore[clID] = current
			demoReviewersMu.Unlock()
		}

		h.emitter.Emit(r.Context(), events.New(
			events.TypeReviewersAssigned,
			"critique",
			strconv.FormatInt(clID, 10),
			events.ReviewersAssignedPayload{
				CLID:      strconv.FormatInt(clID, 10),
				Author:    h.resolveReviewer(r),
				Reviewers: expanded,
				URL:       h.clURL(r, clID),
			},
		))
	}

	h.renderActionsBar(w, r, clID)
}

// HandleRemoveReviewer unassigns a reviewer from a CL.
// POST /cl/{id}/reviewers/{user}/remove
// DELETE /cl/{id}/reviewers/{user}
func (h *CLHandler) HandleRemoveReviewer(w http.ResponseWriter, r *http.Request) {
	clID, err := parseCLID(r)
	if err != nil {
		http.Error(w, "invalid changelist id", http.StatusBadRequest)
		return
	}

	user := r.PathValue("user")
	if user == "" {
		user = r.URL.Query().Get("user")
		if user == "" {
			user = r.FormValue("user")
		}
	}
	user = strings.TrimSpace(user)
	if user == "" {
		http.Error(w, "reviewer username required", http.StatusBadRequest)
		return
	}

	if h.db != nil {
		if err := h.db.RemoveReviewer(r.Context(), clID, user); err != nil {
			log.Printf("failed to remove reviewer: %v", err)
			http.Error(w, "failed to remove reviewer", http.StatusInternalServerError)
			return
		}
	} else {
		demoReviewersMu.Lock()
		current := demoReviewersStore[clID]
		filtered := make([]db.ReviewerAssignment, 0, len(current))
		for _, rev := range current {
			if rev.Reviewer != user {
				filtered = append(filtered, rev)
			}
		}
		demoReviewersStore[clID] = filtered
		demoReviewersMu.Unlock()
	}

	h.renderActionsBar(w, r, clID)
}

// HandleSubmitCL validates approvals and unresolved comments policy before committing a CL to trunk.
// POST /cl/{id}/submit
func (h *CLHandler) HandleSubmitCL(w http.ResponseWriter, r *http.Request) {
	clID, err := parseCLID(r)
	if err != nil {
		http.Error(w, "invalid changelist id", http.StatusBadRequest)
		return
	}

	clStatus, needsRebase, approvalsCount, unresolvedCount, reqApprovals, allowUnresolved, requireGreen, requiredCIRunners, err := h.getSubmitCLMetadata(r.Context(), clID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	if err := h.validateSubmitBlockers(clID, clStatus, needsRebase, allowUnresolved, unresolvedCount); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	justification := strings.TrimSpace(r.FormValue("justification"))
	if len(justification) > 400 {
		http.Error(w, "override justification exceeds 400 characters", http.StatusBadRequest)
		return
	}

	reviewer := h.resolveReviewer(r)
	if err := h.validateSubmitApprovals(clID, reqApprovals, approvalsCount, justification, reviewer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.validateSubmitCI(r.Context(), clID, requireGreen, requiredCIRunners, justification, reviewer); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if h.db != nil {
		if _, err := h.db.SubmitCL(r.Context(), clID); err != nil {
			log.Printf("failed to submit CL %d: %v", clID, err)
			http.Error(w, "failed to submit CL: "+err.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		demoCLStatusMu.Lock()
		demoCLStatusStore[clID] = string(types.CLStatusSubmitted)
		demoCLStatusMu.Unlock()
	}

	h.emitter.Emit(r.Context(), events.New(
		events.TypeCLSubmitted,
		"critique",
		strconv.FormatInt(clID, 10),
		events.CLSubmittedPayload{
			CLID:          strconv.FormatInt(clID, 10),
			Author:        reviewer,
			URL:           h.clURL(r, clID),
			Justification: justification,
		},
	))

	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", fmt.Sprintf("/cl/%d", clID))
		w.WriteHeader(http.StatusOK)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/cl/%d", clID), http.StatusSeeOther)
}

func (h *CLHandler) renderActionsBar(w http.ResponseWriter, r *http.Request, clID int64) {
	author := h.resolveReviewer(r)
	clStatus, approvals, reviewers, approvedCount, unresolvedCount, draftCount, reqApprovals, allowUnresolved := h.fetchActionsBarData(r.Context(), clID, author)

	data := views.CLDetailData{
		CL: &db.Changelist{
			CLID:   clID,
			Status: clStatus,
		},
		Approvals:                 approvals,
		Reviewers:                 reviewers,
		RequiredApprovals:         reqApprovals,
		ApprovedCount:             approvedCount,
		AllowSubmitWithUnresolved: allowUnresolved,
		UnresolvedComments:        unresolvedCount,
		DraftCommentsCount:        draftCount,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	target := r.Header.Get("HX-Target")
	if target == "reviewers-control" {
		_ = views.ReviewersControl(data).Render(r.Context(), w)
		_ = views.SubmitButtonOOB(data).Render(r.Context(), w)
	} else if target == "approvals-control" {
		_ = views.ApprovalsControl(data).Render(r.Context(), w)
		_ = views.SubmitButtonOOB(data).Render(r.Context(), w)
	} else {
		_ = views.CLActionsBar(data).Render(r.Context(), w)
		_ = views.SubmitButtonOOB(data).Render(r.Context(), w)
	}
}

// HandleFileReview marks a file as reviewed or not reviewed for the active reviewer.
// POST /cl/{id}/file-review
func (h *CLHandler) HandleFileReview(w http.ResponseWriter, r *http.Request) {
	clID, err := parseCLID(r)
	if err != nil {
		http.Error(w, "invalid changelist id", http.StatusBadRequest)
		return
	}

	filePath := r.URL.Query().Get("file")
	if filePath == "" {
		filePath = r.FormValue("file")
	}
	if filePath == "" {
		http.Error(w, "missing file parameter", http.StatusBadRequest)
		return
	}

	reviewedStr := r.URL.Query().Get("reviewed")
	if reviewedStr == "" {
		reviewedStr = r.FormValue("reviewed")
	}
	reviewed := true
	if reviewedStr == "false" || reviewedStr == "0" {
		reviewed = false
	}

	psStr := r.URL.Query().Get("ps")
	ps, _ := strconv.Atoi(psStr)
	if ps <= 0 {
		ps = 1
	}
	basePSStr := r.URL.Query().Get("base_ps")
	basePS, _ := strconv.Atoi(basePSStr)

	reviewer := h.resolveReviewer(r)

	if h.db != nil {
		if err := h.db.MarkFileReviewed(r.Context(), clID, filePath, reviewer, reviewed); err != nil {
			log.Printf("failed to mark file reviewed: %v", err)
			http.Error(w, "failed to update review status", http.StatusInternalServerError)
			return
		}
	} else {
		demoFileReviewsMu.Lock()
		key := fmt.Sprintf("%d:%s:%s", clID, reviewer, filePath)
		demoFileReviewsStore[key] = reviewed
		demoFileReviewsMu.Unlock()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.FileReviewBadge(clID, filePath, reviewed, ps, basePS)
	_ = component.Render(r.Context(), w)
}

// HandlePublishReview publishes all draft comments by the active reviewer and applies optional overall summary and verdict.
// POST /cl/{id}/reply
func (h *CLHandler) HandlePublishReview(w http.ResponseWriter, r *http.Request) {
	clID, err := parseCLID(r)
	if err != nil {
		http.Error(w, "invalid changelist id", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}

	author := strings.TrimSpace(r.FormValue("author"))
	if author == "" {
		author = h.resolveReviewer(r)
	}

	overallMsg := strings.TrimSpace(r.FormValue("message"))
	verdict := strings.TrimSpace(r.FormValue("verdict"))

	if h.db != nil {
		h.publishDBReview(r.Context(), clID, author, overallMsg, verdict)
	} else {
		h.publishDemoReview(clID, author, overallMsg, verdict)
	}

	h.emitter.Emit(r.Context(), events.New(
		events.TypeCommentsPublished,
		"critique",
		strconv.FormatInt(clID, 10),
		events.CommentsPublishedPayload{
			CLID:         strconv.FormatInt(clID, 10),
			Author:       author,
			CommentCount: 1,
			URL:          h.clURL(r, clID),
		},
	))

	if parsedVerdict, ok := types.ParseVerdict(verdict); ok && (parsedVerdict.IsApproval() || parsedVerdict == types.VerdictChangesRequested) {
		h.emitter.Emit(r.Context(), events.New(
			events.TypeApprovalUpdated,
			"critique",
			strconv.FormatInt(clID, 10),
			events.ApprovalUpdatedPayload{
				CLID:     strconv.FormatInt(clID, 10),
				Reviewer: author,
				Status:   verdict,
				URL:      h.clURL(r, clID),
			},
		))
	}

	http.Redirect(w, r, fmt.Sprintf("/cl/%d", clID), http.StatusSeeOther)
}

func (h *CLHandler) renderCLPage(w http.ResponseWriter, r *http.Request, clID int64, targetFile string) {
	if h.db == nil {
		h.renderDemoCLPage(w, r, clID, targetFile)
		return
	}

	ctx := r.Context()
	cl, err := h.db.GetCL(ctx, clID)
	if err != nil {
		http.Error(w, fmt.Sprintf("changelist %d not found: %v", clID, err), http.StatusNotFound)
		return
	}

	patchsets, err := h.db.ListPatchsets(ctx, clID)
	if err != nil {
		log.Printf("error listing patchsets for cl %d: %v", clID, err)
	}

	defaultPS := 1
	if len(patchsets) > 0 {
		defaultPS = patchsets[len(patchsets)-1].PatchsetNumber
	}
	currentPS, basePS := parsePatchsetQueryParams(r, defaultPS)
	baseTreeHash, modTreeHash := h.resolveTreeHashes(ctx, cl, currentPS, basePS)

	fetchTree := func(hash []byte) ([]tree.Entry, error) {
		return h.db.GetTreeEntries(ctx, hash)
	}
	files, err := diff.DiffTrees(ctx, fetchTree, baseTreeHash, modTreeHash)
	if err != nil {
		log.Printf("warning: error diffing trees: %v", err)
		files = []diff.FileDiffSummary{}
	}

	allComments, err := h.db.ListAllComments(ctx, clID)
	if err != nil {
		log.Printf("error listing all comments: %v", err)
	}

	reviewer := h.resolveReviewer(r)
	drafts, _ := h.db.ListDraftComments(ctx, clID, reviewer)

	unresolvedTotal := 0
	commentsPerFile := make(map[string]int)
	for _, comment := range allComments {
		if !comment.IsDraft && !comment.Resolved {
			unresolvedTotal++
			commentsPerFile[comment.FilePath]++
		}
	}

	if targetFile != "" {
		_ = h.db.MarkFileReviewed(ctx, clID, targetFile, reviewer, true)
	}
	reviewedMap, _ := h.db.ListReviewedFiles(ctx, clID, reviewer)
	reviewedCount := h.populateFileDiffStats(files, commentsPerFile, reviewedMap)

	var selectedDetail *diff.FileDiffDetail
	commentsByLine := make(map[int][]db.Comment)
	if targetFile != "" {
		selectedDetail, _ = h.resolveFileDiffDetail(ctx, targetFile, baseTreeHash, modTreeHash)
		commentsByLine = h.getFileCommentsByLine(ctx, clID, targetFile, reviewer)
	}

	approvals, err := h.db.ListApprovals(ctx, clID)
	if err != nil {
		log.Printf("error listing approvals: %v", err)
	}
	reviewers, err := h.db.ListReviewers(ctx, clID)
	if err != nil {
		log.Printf("error listing reviewers: %v", err)
	}
	approvedCount, _ := h.db.CountApprovals(ctx, clID)

	reqApprovals, allowUnresolved, _, requiredCIRunners := h.resolveCLPolicy(ctx, cl)
	ciRunners := h.buildCIRunnerItems(ctx, clID, currentPS, requiredCIRunners)

	data := views.CLDetailData{
		CL:                        cl,
		Patchsets:                 patchsets,
		CurrentPatchset:           currentPS,
		BasePatchset:              basePS,
		Files:                     files,
		SelectedFilePath:          targetFile,
		SelectedFileDiff:          selectedDetail,
		CommentsByLine:            commentsByLine,
		Approvals:                 approvals,
		Reviewers:                 reviewers,
		RequiredApprovals:         reqApprovals,
		ApprovedCount:             approvedCount,
		AllowSubmitWithUnresolved: allowUnresolved,
		UnresolvedComments:        unresolvedTotal,
		ActiveReviewer:            reviewer,
		ReviewedFilesCount:        reviewedCount,
		DraftCommentsCount:        len(drafts),
		DraftComments:             drafts,
		CIRunners:                 ciRunners,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.CLDetail(data).Render(ctx, w)
}

func (h *CLHandler) resolveCLPolicy(ctx context.Context, cl *db.Changelist) (reqApprovals int, allowUnresolved bool, requireGreen bool, ciRunners []string) {
	if h.config != nil {
		reqApprovals = h.config.Critique.GetRequiredApprovals()
		allowUnresolved = h.config.Critique.GetAllowSubmitWithUnresolvedComments()
		requireGreen = h.config.Critique.GetRequireGreenCheckRuns()
	}
	if h.db == nil || h.cas == nil || cl == nil || len(cl.RootTreeHash) == 0 {
		return reqApprovals, allowUnresolved, requireGreen, nil
	}

	baseTree := tree.EmptyTreeHash
	if cl.ParentCL != nil && *cl.ParentCL > 0 {
		if parentCL, err := h.db.GetCL(ctx, *cl.ParentCL); err == nil && parentCL != nil && len(parentCL.RootTreeHash) > 0 {
			baseTree = parentCL.RootTreeHash
		}
	}

	fetchTree := func(hash []byte) ([]tree.Entry, error) {
		return h.db.GetTreeEntries(ctx, hash)
	}
	touchedFiles, err := tree.DiffTreeFiles(baseTree, cl.RootTreeHash, fetchTree)
	if err != nil || len(touchedFiles) == 0 {
		return reqApprovals, allowUnresolved, requireGreen, nil
	}

	ownersProvider := func(dir string) ([]byte, error) {
		ownersPath := "OWNERS.toml"
		if dir != "" {
			ownersPath = dir + "/OWNERS.toml"
		}
		entry, err := tree.ResolvePath(cl.RootTreeHash, ownersPath, fetchTree)
		if err != nil || entry == nil || entry.IsDir() || len(entry.BlobSHA256) == 0 {
			return nil, os.ErrNotExist
		}
		return h.readBlobBytes(entry.BlobSHA256)
	}

	evaluator := owners.NewEvaluator(ownersProvider, nil)
	policy, err := evaluator.ResolvePolicy(touchedFiles)
	if err != nil || policy == nil {
		return reqApprovals, allowUnresolved, requireGreen, nil
	}

	reqApprovals, allowUnresolved, requireGreen = mergeOwnersPolicy(policy, reqApprovals, allowUnresolved, requireGreen)
	return reqApprovals, allowUnresolved, requireGreen, policy.CIRunners
}

func (h *CLHandler) readBlobBytes(sha256Bytes []byte) ([]byte, error) {
	if len(sha256Bytes) == 0 || bytes.Equal(sha256Bytes, make([]byte, 32)) {
		return []byte{}, nil
	}
	f, _, err := h.cas.OpenBlob(sha256Bytes)
	if err != nil {
		return []byte{}, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func parseCLID(r *http.Request) (int64, error) {
	idStr := r.PathValue("id")
	if idStr == "" {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) >= 3 && parts[1] == "cl" {
			idStr = parts[2]
		}
	}
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *CLHandler) renderDemoCLPage(w http.ResponseWriter, r *http.Request, clID int64, targetFile string) {
	currentUser := auth.CurrentOSUser()
	demoCLStatus := "shelved"
	demoCLStatusMu.RLock()
	if s, ok := demoCLStatusStore[clID]; ok && s != "" {
		demoCLStatus = s
	}
	demoCLStatusMu.RUnlock()

	demoCL := &db.Changelist{
		CLID:        clID,
		Author:      fmt.Sprintf("%s@corp", currentUser),
		Description: "Critique: fast server-authoritative code review web UI and diff visualizer\n\n- Pure Go + a-h/templ + HTMX + Tailwind CSS\n- Myers diff visualizer with Chroma syntax highlighting\n- Inline comment threads and reviewer verdicts\n- Live reload via air and templ proxy",
		Status:      demoCLStatus,
		CommittedAt: time.Now().Add(-2 * time.Hour),
	}

	patchsets := []db.Patchset{
		{PatchsetID: 1, CLID: clID, PatchsetNumber: 1, RootTreeHash: []byte{1, 2, 3}},
		{PatchsetID: 2, CLID: clID, PatchsetNumber: 2, RootTreeHash: []byte{4, 5, 6}},
	}

	currentPS, basePS := parsePatchsetQueryParams(r, 2)

	files := []diff.FileDiffSummary{
		{Path: "internal/web/diff/engine.go", Type: "M"},
		{Path: "internal/web/views/diff_view.templ", Type: "A"},
		{Path: "cmd/critique/main.go", Type: "M"},
		{Path: "assets/css/input.css", Type: "M"},
	}

	for i := range files {
		if d, err := computeDemoFileDiff(files[i].Path, files[i].Type); err == nil && d != nil {
			files[i].Additions = d.Additions
			files[i].Deletions = d.Deletions
		}
	}

	reviewer := h.resolveReviewer(r)
	demoFileReviewsMu.Lock()
	if targetFile != "" {
		demoFileReviewsStore[fmt.Sprintf("%d:%s:%s", clID, reviewer, targetFile)] = true
	}
	reviewedCount := 0
	for i := range files {
		key := fmt.Sprintf("%d:%s:%s", clID, reviewer, files[i].Path)
		files[i].Reviewed = demoFileReviewsStore[key]
		if files[i].Reviewed {
			reviewedCount++
		}
	}
	demoFileReviewsMu.Unlock()

	var detail *diff.FileDiffDetail
	if targetFile != "" {
		changeType := "M"
		for _, file := range files {
			if file.Path == targetFile {
				changeType = file.Type
				break
			}
		}
		detail, _ = computeDemoFileDiff(targetFile, changeType)
	}

	commentsByLine, unresolvedCount, reviewerDrafts := resolveDemoCLComments(clID, targetFile, reviewer)

	// Seed an initial demo comment if store is completely empty
	if len(demoCommentsStore) == 0 && targetFile == "internal/web/diff/engine.go" {
		sampleComment := db.Comment{
			ID:             1,
			CLID:           clID,
			PatchsetNumber: 1,
			FilePath:       targetFile,
			LineNumber:     13,
			EndLineNumber:  16,
			Side:           "modified",
			Author:         currentUser,
			Body:           "LGTM! The Myers diff traversal and Chroma tokenization align cleanly with Google Critique.",
			Resolved:       false,
			IsDraft:        false,
			CreatedAt:      time.Now().Add(-30 * time.Minute),
		}
		commentsByLine[16] = []db.Comment{sampleComment}
		demoCommentsMu.Lock()
		key := fmt.Sprintf("%d:%s:%d:%s", clID, targetFile, 16, "modified")
		demoCommentsStore[key] = []db.Comment{sampleComment}
		demoCommentsMu.Unlock()
		unresolvedCount = 1
	}

	demoApprovalsMu.RLock()
	approvals := demoApprovalsStore[clID]
	demoApprovalsMu.RUnlock()
	approvedCount := countApprovedVerdicts(approvals)

	demoReviewersMu.RLock()
	rawReviewers := demoReviewersStore[clID]
	demoReviewersMu.RUnlock()
	reviewerList := consolidateReviewersWithApprovals(rawReviewers, approvals, clID)

	reqApprovals := 0
	allowUnresolved := false
	if h.config != nil {
		reqApprovals = h.config.Critique.GetRequiredApprovals()
		allowUnresolved = h.config.Critique.GetAllowSubmitWithUnresolvedComments()
	}

	ciRunners := h.buildCIRunnerItems(r.Context(), demoCL.CLID, currentPS, nil)

	data := views.CLDetailData{
		CL:                        demoCL,
		Patchsets:                 patchsets,
		CurrentPatchset:           currentPS,
		BasePatchset:              basePS,
		Files:                     files,
		SelectedFilePath:          targetFile,
		SelectedFileDiff:          detail,
		CommentsByLine:            commentsByLine,
		Approvals:                 approvals,
		Reviewers:                 reviewerList,
		RequiredApprovals:         reqApprovals,
		ApprovedCount:             approvedCount,
		AllowSubmitWithUnresolved: allowUnresolved,
		UnresolvedComments:        unresolvedCount,
		ActiveReviewer:            reviewer,
		ReviewedFilesCount:        reviewedCount,
		DraftCommentsCount:        len(reviewerDrafts),
		DraftComments:             reviewerDrafts,
		CIRunners:                 ciRunners,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.CLDetail(data).Render(r.Context(), w)
}

func (h *CLHandler) renderCLFileDiffPage(w http.ResponseWriter, r *http.Request, clID int64, targetFile string) {
	ctx := r.Context()
	cl, err := h.db.GetCL(ctx, clID)
	if err != nil || cl == nil {
		http.Error(w, "changelist not found", http.StatusNotFound)
		return
	}

	patchsets, _ := h.db.ListPatchsets(ctx, clID)
	defaultPS := 1
	if len(patchsets) > 0 {
		defaultPS = patchsets[len(patchsets)-1].PatchsetNumber
	}
	currentPS, basePS := parsePatchsetQueryParams(r, defaultPS)
	reviewer := h.resolveReviewer(r)
	drafts, _ := h.db.ListDraftComments(ctx, clID, reviewer)

	baseTreeHash, modTreeHash := h.resolveTreeHashes(ctx, cl, currentPS, basePS)
	detail, err := h.resolveFileDiffDetail(ctx, targetFile, baseTreeHash, modTreeHash)
	if err != nil {
		log.Printf("error computing side-by-side diff: %v", err)
	}

	changeType := "M"
	if detail != nil && detail.Type != "" {
		changeType = detail.Type
	}

	if targetFile != "" {
		_ = h.db.MarkFileReviewed(ctx, clID, targetFile, reviewer, true)
	}

	reviewedMap, _ := h.db.ListReviewedFiles(ctx, clID, reviewer)
	isReviewed := false
	if reviewedMap != nil {
		isReviewed = reviewedMap[targetFile]
	}

	commentsByLine := h.getFileCommentsByLine(ctx, clID, targetFile, reviewer)

	data := views.FileDiffPageData{
		CLID:               clID,
		CurrentPatchset:    currentPS,
		BasePatchset:       basePS,
		FilePath:           targetFile,
		ChangeType:         changeType,
		Reviewed:           isReviewed,
		Detail:             detail,
		CommentsByLine:     commentsByLine,
		DraftCommentsCount: len(drafts),
		DraftComments:      drafts,
		ActiveReviewer:     reviewer,
		Patchsets:          patchsets,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.FileDiffPage(data).Render(ctx, w)
}

func (h *CLHandler) renderDemoCLFileDiffPage(w http.ResponseWriter, r *http.Request, clID int64, targetFile string) {
	currentPS := 2
	if psQuery := r.URL.Query().Get("ps"); psQuery != "" {
		if parsed, err := strconv.Atoi(psQuery); err == nil && parsed > 0 {
			currentPS = parsed
		}
	}
	basePS := 0
	if basePSQuery := r.URL.Query().Get("base_ps"); basePSQuery != "" {
		if parsed, err := strconv.Atoi(basePSQuery); err == nil && parsed >= 0 {
			basePS = parsed
		}
	}

	patchsets := []db.Patchset{
		{PatchsetID: 1, CLID: clID, PatchsetNumber: 1, RootTreeHash: []byte{1, 2, 3}},
		{PatchsetID: 2, CLID: clID, PatchsetNumber: 2, RootTreeHash: []byte{4, 5, 6}},
	}

	changeType := "M"
	if targetFile == "internal/web/views/diff_view.templ" {
		changeType = "A"
	}

	detail, _ := computeDemoFileDiff(targetFile, changeType)

	reviewer := h.resolveReviewer(r)
	demoFileReviewsMu.Lock()
	if targetFile != "" {
		demoFileReviewsStore[fmt.Sprintf("%d:%s:%s", clID, reviewer, targetFile)] = true
	}
	key := fmt.Sprintf("%d:%s:%s", clID, reviewer, targetFile)
	isReviewed := demoFileReviewsStore[key]
	demoFileReviewsMu.Unlock()

	demoCommentsMu.RLock()
	commentsByLine := make(map[int][]db.Comment)
	var reviewerDrafts []db.Comment
	for k, list := range demoCommentsStore {
		for _, comment := range list {
			if comment.CLID == clID && comment.Author == reviewer && comment.IsDraft {
				reviewerDrafts = append(reviewerDrafts, comment)
			}
			if strings.HasPrefix(k, fmt.Sprintf("%d:%s:", clID, targetFile)) {
				if !comment.IsDraft || comment.Author == reviewer {
					anchorLine := comment.EndLineNumber
					if anchorLine <= 0 {
						anchorLine = comment.LineNumber
					}
					commentsByLine[anchorLine] = append(commentsByLine[anchorLine], comment)
				}
			}
		}
	}
	demoCommentsMu.RUnlock()

	data := views.FileDiffPageData{
		CLID:               clID,
		CurrentPatchset:    currentPS,
		BasePatchset:       basePS,
		FilePath:           targetFile,
		ChangeType:         changeType,
		Reviewed:           isReviewed,
		Detail:             detail,
		CommentsByLine:     commentsByLine,
		DraftCommentsCount: len(reviewerDrafts),
		DraftComments:      reviewerDrafts,
		ActiveReviewer:     reviewer,
		Patchsets:          patchsets,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.FileDiffPage(data).Render(r.Context(), w)
}

func (h *CLHandler) renderCLDiffContent(w http.ResponseWriter, r *http.Request, clID int64, targetFile string) {
	if h.db == nil {
		h.renderDemoCLDiffContent(w, r, clID, targetFile)
		return
	}

	ctx := r.Context()
	cl, err := h.db.GetCL(ctx, clID)
	if err != nil {
		http.Error(w, fmt.Sprintf("changelist %d not found: %v", clID, err), http.StatusNotFound)
		return
	}

	patchsets, _ := h.db.ListPatchsets(ctx, clID)
	defaultPS := 1
	if len(patchsets) > 0 {
		defaultPS = patchsets[len(patchsets)-1].PatchsetNumber
	}
	currentPS, basePS := parsePatchsetQueryParams(r, defaultPS)
	baseTreeHash, modTreeHash := h.resolveTreeHashes(ctx, cl, currentPS, basePS)

	detail, err := h.resolveFileDiffDetail(ctx, targetFile, baseTreeHash, modTreeHash)
	if err != nil {
		http.Error(w, fmt.Sprintf("error computing diff for %s: %v", targetFile, err), http.StatusInternalServerError)
		return
	}

	reviewer := h.resolveReviewer(r)
	commentsByLine := h.getFileCommentsByLine(ctx, clID, targetFile, reviewer)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.DiffView(clID, currentPS, basePS, detail, commentsByLine).Render(r.Context(), w)
}

func (h *CLHandler) renderDemoCLDiffContent(w http.ResponseWriter, r *http.Request, clID int64, targetFile string) {
	currentPS := 2
	if psQuery := r.URL.Query().Get("ps"); psQuery != "" {
		if parsed, err := strconv.Atoi(psQuery); err == nil && parsed > 0 {
			currentPS = parsed
		}
	}
	basePS := 0
	if basePSQuery := r.URL.Query().Get("base_ps"); basePSQuery != "" {
		if parsed, err := strconv.Atoi(basePSQuery); err == nil && parsed >= 0 {
			basePS = parsed
		}
	}

	changeType := "M"
	if targetFile == "internal/web/views/diff_view.templ" {
		changeType = "A"
	}

	detail, err := computeDemoFileDiff(targetFile, changeType)
	if err != nil {
		http.Error(w, fmt.Sprintf("error computing demo diff for %s: %v", targetFile, err), http.StatusInternalServerError)
		return
	}

	reviewer := h.resolveReviewer(r)
	demoCommentsMu.RLock()
	commentsByLine := make(map[int][]db.Comment)
	for key, list := range demoCommentsStore {
		for _, comment := range list {
			if strings.HasPrefix(key, fmt.Sprintf("%d:%s:", clID, targetFile)) {
				if !comment.IsDraft || comment.Author == reviewer {
					anchorLine := comment.EndLineNumber
					if anchorLine <= 0 {
						anchorLine = comment.LineNumber
					}
					commentsByLine[anchorLine] = append(commentsByLine[anchorLine], comment)
				}
			}
		}
	}
	demoCommentsMu.RUnlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.DiffView(clID, currentPS, basePS, detail, commentsByLine).Render(r.Context(), w)
}

func computeDemoFileDiff(targetFile string, changeType string) (*diff.FileDiffDetail, error) {
	baseData := []byte(`package diff

import (
	"context"
	"fmt"
)

// DiffTrees performs diff between two trees.
func DiffTrees(ctx context.Context, baseHash, modHash []byte) ([]FileDiffSummary, error) {
	fmt.Println("Basic diff traversal")
	return nil, nil
}
`)

	modData := []byte(`package diff

import (
	"context"
	"fmt"
	"sort"
	"niebla/pkg/tree"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// DiffTrees performs recursive Merkle tree traversal between base and mod revisions.
func DiffTrees(ctx context.Context, fetchTree tree.FetchTreeFunc, baseHash, modHash []byte) ([]FileDiffSummary, error) {
	var results []FileDiffSummary
	fmt.Println("Advanced server-authoritative Merkle diff")
	dmp := diffmatchpatch.New()
	_ = dmp
	sort.Slice(results, func(i, j int) bool {
		return results[i].Path < results[j].Path
	})
	return results, nil
}
`)

	if diskData, err := os.ReadFile(targetFile); err == nil && len(diskData) > 0 {
		modData = diskData
		lines := strings.Split(string(diskData), "\n")
		if len(lines) > 20 {
			baseLines := append(lines[:10], lines[len(lines)-10:]...)
			baseData = []byte(strings.Join(baseLines, "\n"))
		}
	}

	if targetFile == "internal/web/views/diff_view.templ" || changeType == "A" {
		changeType = "A"
		baseData = nil
	}

	detail, err := diff.ComputeSideBySideDiff(targetFile, baseData, modData, changeType)
	if err == nil && detail != nil {
		detail.BaseBlobHex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		detail.ModBlobHex = "8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4"
	}
	return detail, err
}

func (h *CLHandler) buildCIRunnerItems(ctx context.Context, clID int64, patchsetNum int, requiredCIRunners []string) []views.CIRunnerItem {
	var checkRuns []db.CheckRun
	if h.db != nil {
		if rList, err := h.db.ListCheckRunsForPatchset(ctx, clID, patchsetNum); err == nil {
			checkRuns = rList
		}
	} else {
		demoCheckRunsMu.RLock()
		checkRuns = demoCheckRunsStore[fmt.Sprintf("%d:%d", clID, patchsetNum)]
		demoCheckRunsMu.RUnlock()
	}

	reqSet := make(map[string]bool, len(requiredCIRunners))
	for _, runner := range requiredCIRunners {
		reqSet[strings.ToLower(strings.TrimSpace(runner))] = true
	}

	seenRunners := make(map[string]bool, len(checkRuns)+len(requiredCIRunners))
	var runnerItems []views.CIRunnerItem

	for _, cr := range checkRuns {
		norm := strings.ToLower(strings.TrimSpace(cr.RunnerName))
		seenRunners[norm] = true

		var logHex string
		if len(cr.LogBlobSHA256) > 0 {
			logHex = hex.EncodeToString(cr.LogBlobSHA256)
		}

		runnerItems = append(runnerItems, views.CIRunnerItem{
			RunnerName:  cr.RunnerName,
			Provider:    h.resolveRunnerProvider(cr.RunnerName, cr.Provider),
			Status:      strings.ToLower(cr.Status),
			IsRequired:  reqSet[norm],
			ExternalURL: cr.ExternalURL,
			LogBlobHex:  logHex,
			Duration:    formatCheckRunDuration(cr),
		})
	}

	for _, reqName := range requiredCIRunners {
		norm := strings.ToLower(strings.TrimSpace(reqName))
		if !seenRunners[norm] {
			runnerItems = append(runnerItems, views.CIRunnerItem{
				RunnerName: reqName,
				Provider:   h.resolveRunnerProvider(reqName, ""),
				Status:     string(types.CheckRunStatusQueued),
				IsRequired: true,
			})
		}
	}

	return runnerItems
}
