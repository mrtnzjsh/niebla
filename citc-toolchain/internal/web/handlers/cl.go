package handlers

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"citc-toolchain/internal/db"
	"citc-toolchain/internal/web/diff"
	"citc-toolchain/internal/web/views"
	"citc-toolchain/pkg/cas"
	"citc-toolchain/pkg/tree"
)

// CLHandler handles changelist reviews, patchset switching, diff views, and reviewer verdicts.
type CLHandler struct {
	db  *db.DB
	cas *cas.Storage
}

// NewCLHandler initializes a new CLHandler.
func NewCLHandler(database *db.DB, casStorage *cas.Storage) *CLHandler {
	return &CLHandler{
		db:  database,
		cas: casStorage,
	}
}

// HandleIndex lists changelists or redirects to latest changelist.
// GET /
func (h *CLHandler) HandleIndex(w http.ResponseWriter, r *http.Request) {
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

	reviewer := r.Header.Get("X-CitC-Reviewer")
	if reviewer == "" {
		reviewer = "matatan"
	}

	if err := h.db.SetApproval(r.Context(), clID, reviewer, verdict); err != nil {
		log.Printf("failed to set approval: %v", err)
		http.Error(w, "failed to record approval", http.StatusInternalServerError)
		return
	}

	approvals, err := h.db.ListApprovals(r.Context(), clID)
	if err != nil {
		log.Printf("failed to list approvals: %v", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.ApprovalButtons(clID, approvals)
	_ = component.Render(r.Context(), w)
}

func (h *CLHandler) renderCLPage(w http.ResponseWriter, r *http.Request, clID int64, targetFile string) {
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

	unresolvedTotal := 0
	commentsPerFile := make(map[string]int)
	for _, c := range allComments {
		if !c.Resolved {
			unresolvedTotal++
			commentsPerFile[c.FilePath]++
		}
	}

	// 5. Populate file additions & deletions
	for i := range files {
		files[i].UnresolvedComments = commentsPerFile[files[i].Path]
		baseData, _ := h.readBlobBytes(files[i].BaseBlobSHA)
		modData, _ := h.readBlobBytes(files[i].ModifiedBlobSHA)
		d, err := diff.ComputeSideBySideDiff(files[i].Path, baseData, modData, files[i].Type)
		if err == nil && d != nil {
			files[i].Additions = d.Additions
			files[i].Deletions = d.Deletions
		}
	}

	// 6. Select active file diff
	if targetFile == "" && len(files) > 0 {
		targetFile = files[0].Path
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

		// Filter comments for this selected file
		fileComments, err := h.db.ListComments(ctx, clID, targetFile)
		if err == nil {
			for _, c := range fileComments {
				commentsByLine[c.LineNumber] = append(commentsByLine[c.LineNumber], c)
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
