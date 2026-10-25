package handlers

import (
	"bytes"
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

	"citc-toolchain/internal/db"
	"citc-toolchain/internal/web/auth"
	"citc-toolchain/internal/web/diff"
	"citc-toolchain/internal/web/views"
	"citc-toolchain/pkg/cas"
	"citc-toolchain/pkg/tree"
)

var (
	demoApprovalsMu      sync.RWMutex
	demoApprovalsStore   = make(map[int64][]db.Approval)
	demoFileReviewsMu    sync.RWMutex
	demoFileReviewsStore = make(map[string]bool)
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
}

// CLHandler handles changelist reviews, patchset switching, diff views, and reviewer verdicts.
type CLHandler struct {
	db           *db.DB
	cas          *cas.Storage
	authResolver auth.UserIdentityResolver
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
	}
}

func (h *CLHandler) resolveReviewer(r *http.Request) string {
	if h.authResolver != nil {
		return h.authResolver.ResolveUser(r)
	}
	if r != nil {
		if q := r.URL.Query().Get("reviewer"); q != "" {
			return strings.TrimSpace(q)
		}
	}
	return auth.CurrentOSUser()
}

// HandleIndex lists changelists or redirects to latest changelist.
// GET /
func (h *CLHandler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		currentUser := auth.CurrentOSUser()
		demoCLs := []db.Changelist{
			{
				CLID:        1,
				Author:      fmt.Sprintf("%s@corp", currentUser),
				Description: "feat(critique): implement fast server-authoritative code review web UI and Myers diff engine",
				Status:      "shelved",
				CommittedAt: time.Now().Add(-2 * time.Hour),
			},
			{
				CLID:        2,
				Author:      "developer@corp",
				Description: "cas: optimize chunk streaming and LRU tree node caching",
				Status:      "submitted",
				CommittedAt: time.Now().Add(-24 * time.Hour),
			},
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = views.CLList(demoCLs).Render(r.Context(), w)
		return
	}

	cls, err := h.db.ListCLs(r.Context(), 50)
	if err != nil {
		log.Printf("error listing changelists: %v", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.CLList(cls)
	_ = component.Render(r.Context(), w)
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

// HandleCLFileDiff renders the review page focused on a specific file path.
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

	h.renderCLPage(w, r, clID, selectedFile)
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
	if verdict != "approved" && verdict != "lgtm" && verdict != "changes_requested" {
		http.Error(w, "invalid verdict value (must be approved, lgtm, or changes_requested)", http.StatusBadRequest)
		return
	}

	reviewer := h.resolveReviewer(r)

	var approvals []db.Approval
	if h.db != nil {
		if err := h.db.SetApproval(r.Context(), clID, reviewer, verdict); err != nil {
			log.Printf("failed to set approval: %v", err)
			http.Error(w, "failed to record approval", http.StatusInternalServerError)
			return
		}

		var err error
		approvals, err = h.db.ListApprovals(r.Context(), clID)
		if err != nil {
			log.Printf("failed to list approvals: %v", err)
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
		approvals = list
		demoApprovalsMu.Unlock()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.ApprovalButtons(clID, approvals)
	_ = component.Render(r.Context(), w)
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
		// 1. Publish all draft comments
		if _, err := h.db.PublishDraftComments(r.Context(), clID, author); err != nil {
			log.Printf("error publishing drafts: %v", err)
		}

		// 2. If overall message is provided, create a top-level review comment
		if overallMsg != "" {
			_, _ = h.db.CreateComment(r.Context(), clID, 1, "", 0, 0, "modified", author, overallMsg, nil, false)
		}

		// 3. If a verdict was submitted, record approval
		if verdict == "lgtm" || verdict == "approved" || verdict == "changes_requested" {
			_ = h.db.SetApproval(r.Context(), clID, author, verdict)
		}
	} else {
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
			c := db.Comment{
				ID:             nextCommentID,
				CLID:           clID,
				PatchsetNumber: 1,
				FilePath:       "",
				LineNumber:     0,
				EndLineNumber:  0,
				Side:           "modified",
				Author:         author,
				Body:           overallMsg,
				Resolved:       false,
				IsDraft:        false,
				CreatedAt:      time.Now(),
			}
			key := fmt.Sprintf("%d::0:modified", clID)
			demoCommentsStore[key] = append(demoCommentsStore[key], c)
		}
		demoCommentsMu.Unlock()

		if verdict == "lgtm" || verdict == "approved" || verdict == "changes_requested" {
			demoApprovalsMu.Lock()
			list := demoApprovalsStore[clID]
			updated := false
			for i := range list {
				if list[i].Reviewer == author {
					list[i].Verdict = verdict
					list[i].UpdatedAt = time.Now()
					updated = true
					break
				}
			}
			if !updated {
				list = append(list, db.Approval{
					CLID:      clID,
					Reviewer:  author,
					Verdict:   verdict,
					UpdatedAt: time.Now(),
				})
			}
			demoApprovalsStore[clID] = list
			demoApprovalsMu.Unlock()
		}
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

	// 1. Determine active patchset and base patchset
	currentPS := 1
	if len(patchsets) > 0 {
		currentPS = patchsets[len(patchsets)-1].PatchsetNumber
	}
	if psQuery := r.URL.Query().Get("ps"); psQuery != "" {
		if parsed, err := strconv.Atoi(psQuery); err == nil && parsed > 0 {
			currentPS = parsed
		}
	}

	basePS := 0 // 0 = Base CL
	if basePSQuery := r.URL.Query().Get("base_ps"); basePSQuery != "" {
		if parsed, err := strconv.Atoi(basePSQuery); err == nil && parsed >= 0 {
			basePS = parsed
		}
	}

	// 2. Resolve tree hashes for current patchset and base patchset
	var modTreeHash []byte
	psObj, psErr := h.db.GetPatchset(ctx, clID, currentPS)
	if psErr == nil && len(psObj.RootTreeHash) > 0 {
		modTreeHash = psObj.RootTreeHash
	} else {
		modTreeHash = cl.RootTreeHash
	}

	var baseTreeHash []byte
	if basePS == 0 {
		// Base CL
		if cl.ParentCL != nil && *cl.ParentCL > 0 {
			parentCL, parentErr := h.db.GetCL(ctx, *cl.ParentCL)
			if parentErr == nil && len(parentCL.RootTreeHash) > 0 {
				baseTreeHash = parentCL.RootTreeHash
			}
		}
		if len(baseTreeHash) == 0 {
			baseTreeHash = tree.EmptyTreeHash
		}
	} else {
		basePSObj, basePSErr := h.db.GetPatchset(ctx, clID, basePS)
		if basePSErr == nil && len(basePSObj.RootTreeHash) > 0 {
			baseTreeHash = basePSObj.RootTreeHash
		} else {
			baseTreeHash = tree.EmptyTreeHash
		}
	}

	// 3. Diff trees
	fetchTree := func(hash []byte) ([]tree.Entry, error) {
		return h.db.GetTreeEntries(ctx, hash)
	}

	files, err := diff.DiffTrees(ctx, fetchTree, baseTreeHash, modTreeHash)
	if err != nil {
		log.Printf("warning: error diffing trees: %v", err)
		files = []diff.FileDiffSummary{}
	}

	// 4. Fetch all comments for CL and compute counts
	allComments, err := h.db.ListAllComments(ctx, clID)
	if err != nil {
		log.Printf("error listing all comments: %v", err)
	}

	reviewer := h.resolveReviewer(r)
	drafts, _ := h.db.ListDraftComments(ctx, clID, reviewer)

	unresolvedTotal := 0
	commentsPerFile := make(map[string]int)
	for _, c := range allComments {
		if !c.IsDraft && !c.Resolved {
			unresolvedTotal++
			commentsPerFile[c.FilePath]++
		}
	}

	// 5. Select active file diff
	if targetFile == "" && len(files) > 0 {
		targetFile = files[0].Path
	}

	if targetFile != "" {
		_ = h.db.MarkFileReviewed(ctx, clID, targetFile, reviewer, true)
	}

	reviewedMap, _ := h.db.ListReviewedFiles(ctx, clID, reviewer)
	if reviewedMap == nil {
		reviewedMap = make(map[string]bool)
	}

	reviewedCount := 0
	// 6. Populate file additions, deletions & reviewed status
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

	var selectedDetail *diff.FileDiffDetail
	commentsByLine := make(map[int][]db.Comment)

	if targetFile != "" {
		var selectedSummary *diff.FileDiffSummary
		for _, f := range files {
			if f.Path == targetFile {
				selectedSummary = &f
				break
			}
		}

		var baseBlobSHA, modBlobSHA []byte
		changeType := "M"
		if selectedSummary != nil {
			baseBlobSHA = selectedSummary.BaseBlobSHA
			modBlobSHA = selectedSummary.ModifiedBlobSHA
			changeType = selectedSummary.Type
		} else {
			// Resolve manually from trees if not found in summary
			baseEntry, _ := tree.ResolvePath(baseTreeHash, targetFile, fetchTree)
			if baseEntry != nil {
				baseBlobSHA = baseEntry.BlobSHA256
			}
			modEntry, _ := tree.ResolvePath(modTreeHash, targetFile, fetchTree)
			if modEntry != nil {
				modBlobSHA = modEntry.BlobSHA256
			}
		}

		baseBytes, _ := h.readBlobBytes(baseBlobSHA)
		modBytes, _ := h.readBlobBytes(modBlobSHA)

		detail, err := diff.ComputeSideBySideDiff(targetFile, baseBytes, modBytes, changeType)
		if err == nil && detail != nil {
			if len(baseBlobSHA) > 0 {
				detail.BaseBlobHex = hex.EncodeToString(baseBlobSHA)
			}
			if len(modBlobSHA) > 0 {
				detail.ModBlobHex = hex.EncodeToString(modBlobSHA)
			}
			selectedDetail = detail
		}

		// Filter comments for this selected file (published or authored by reviewer)
		fileComments, err := h.db.ListComments(ctx, clID, targetFile)
		if err == nil {
			for _, c := range fileComments {
				if !c.IsDraft || c.Author == reviewer {
					anchorLine := c.EndLineNumber
					if anchorLine <= 0 {
						anchorLine = c.LineNumber
					}
					commentsByLine[anchorLine] = append(commentsByLine[anchorLine], c)
				}
			}
		}
	}

	// 7. Approvals
	approvals, err := h.db.ListApprovals(ctx, clID)
	if err != nil {
		log.Printf("error listing approvals: %v", err)
	}

	data := views.CLDetailData{
		CL:                 cl,
		Patchsets:          patchsets,
		CurrentPatchset:    currentPS,
		BasePatchset:       basePS,
		Files:              files,
		SelectedFilePath:   targetFile,
		SelectedFileDiff:   selectedDetail,
		CommentsByLine:     commentsByLine,
		Approvals:          approvals,
		UnresolvedComments: unresolvedTotal,
		ActiveReviewer:     reviewer,
		ReviewedFilesCount: reviewedCount,
		DraftCommentsCount: len(drafts),
		DraftComments:      drafts,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.CLDetail(data)
	_ = component.Render(ctx, w)
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
	demoCL := &db.Changelist{
		CLID:        clID,
		Author:      fmt.Sprintf("%s@corp", currentUser),
		Description: "Critique: fast server-authoritative code review web UI and diff visualizer\n\n- Pure Go + a-h/templ + HTMX + Tailwind CSS\n- Myers diff visualizer with Chroma syntax highlighting\n- Inline comment threads and reviewer verdicts\n- Live reload via air and templ proxy",
		Status:      "shelved",
		CommittedAt: time.Now().Add(-2 * time.Hour),
	}

	patchsets := []db.Patchset{
		{PatchsetID: 1, CLID: clID, PatchsetNumber: 1, RootTreeHash: []byte{1, 2, 3}},
		{PatchsetID: 2, CLID: clID, PatchsetNumber: 2, RootTreeHash: []byte{4, 5, 6}},
	}

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

	files := []diff.FileDiffSummary{
		{Path: "internal/web/diff/engine.go", Type: "M", Additions: 42, Deletions: 12},
		{Path: "internal/web/views/diff_view.templ", Type: "A", Additions: 135, Deletions: 0},
		{Path: "cmd/critique/main.go", Type: "M", Additions: 68, Deletions: 4},
		{Path: "assets/css/input.css", Type: "M", Additions: 15, Deletions: 3},
	}

	if targetFile == "" {
		targetFile = "internal/web/diff/engine.go"
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
	"citc-toolchain/pkg/tree"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// DiffTrees performs recursive Merkle tree traversal between base and mod revisions.
func DiffTrees(ctx context.Context, fetchTree tree.FetchTreeFunc, baseHash, modHash []byte) ([]FileDiffSummary, error) {
	var results []FileDiffSummary
	fmt.Println("Advanced server-authoritative Merkle diff")
	// Optimized Myers diff with side-by-side row alignment
	dmp := diffmatchpatch.New()
	_ = dmp
	sort.Slice(results, func(i, j int) bool {
		return results[i].Path < results[j].Path
	})
	return results, nil
}
`)

	// Read actual file from disk if present
	if diskData, err := os.ReadFile(targetFile); err == nil && len(diskData) > 0 {
		modData = diskData
		lines := strings.Split(string(diskData), "\n")
		if len(lines) > 20 {
			baseLines := append(lines[:10], lines[len(lines)-10:]...)
			baseData = []byte(strings.Join(baseLines, "\n"))
		}
	}

	changeType := "M"
	for _, f := range files {
		if f.Path == targetFile {
			changeType = f.Type
			break
		}
	}

	detail, _ := diff.ComputeSideBySideDiff(targetFile, baseData, modData, changeType)
	if detail != nil {
		detail.BaseBlobHex = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		detail.ModBlobHex = "8f434346648f6b96df89dda901c5176b10a6d83961dd3c1ac88b59b2dc327aa4"
	}

	demoCommentsMu.RLock()
	commentsByLine := make(map[int][]db.Comment)
	unresolvedCount := 0
	var reviewerDrafts []db.Comment
	for key, list := range demoCommentsStore {
		for _, c := range list {
			if c.CLID == clID && c.Author == reviewer && c.IsDraft {
				reviewerDrafts = append(reviewerDrafts, c)
			}
			if !c.IsDraft && !c.Resolved {
				unresolvedCount++
			}
			if strings.HasPrefix(key, fmt.Sprintf("%d:%s:", clID, targetFile)) {
				if !c.IsDraft || c.Author == reviewer {
					anchorLine := c.EndLineNumber
					if anchorLine <= 0 {
						anchorLine = c.LineNumber
					}
					commentsByLine[anchorLine] = append(commentsByLine[anchorLine], c)
				}
			}
		}
	}
	demoCommentsMu.RUnlock()

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

	data := views.CLDetailData{
		CL:                 demoCL,
		Patchsets:          patchsets,
		CurrentPatchset:    currentPS,
		BasePatchset:       basePS,
		Files:              files,
		SelectedFilePath:   targetFile,
		SelectedFileDiff:   detail,
		CommentsByLine:     commentsByLine,
		Approvals:          approvals,
		UnresolvedComments: unresolvedCount,
		ActiveReviewer:     reviewer,
		ReviewedFilesCount: reviewedCount,
		DraftCommentsCount: len(reviewerDrafts),
		DraftComments:      reviewerDrafts,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.CLDetail(data).Render(r.Context(), w)
}
