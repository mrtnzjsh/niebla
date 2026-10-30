package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"niebla/internal/config"
	"niebla/internal/db"
	"niebla/internal/web/diff"
	"niebla/internal/web/views"
	"niebla/pkg/ai"
	"niebla/pkg/httpclient"
	"niebla/pkg/tree"
)

// HandleGenerateAIHint handles POST /cl/{id}/ai-hint/generate.
// It extracts the target lines, escalates context aperture if needed, queries the configured
// OpenAI-compatible endpoint, and renders the AIHintPreviewCard HTML fragment.
func (h *CommentsHandler) HandleGenerateAIHint(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "failed to parse form", http.StatusBadRequest)
		return
	}

	clIDStr := r.FormValue("cl_id")
	if clIDStr == "" {
		clIDStr = r.PathValue("id")
	}
	clID, err := strconv.ParseInt(clIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid cl_id", http.StatusBadRequest)
		return
	}

	filePath := r.FormValue("file_path")
	line, _ := strconv.Atoi(r.FormValue("line_number"))
	if line <= 0 {
		line = 1
	}
	endLine, _ := strconv.Atoi(r.FormValue("end_line_number"))
	if endLine < line {
		endLine = line
	}
	psNum, _ := strconv.Atoi(r.FormValue("patchset_number"))
	if psNum <= 0 {
		psNum = 1
	}
	commentBody := strings.TrimSpace(r.FormValue("body"))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if commentBody == "" {
		_ = views.AIHintPreviewCard(clID, filePath, endLine, "", "", line, endLine, "", "", "Please write a comment describing the desired change before generating an AI hint.").Render(r.Context(), w)
		return
	}

	if !h.isAIHintConfigured() {
		http.Error(w, "AI hints are not configured (set [critique.ai] endpoint in config.toml)", http.StatusBadRequest)
		return
	}

	cfg := h.config
	if cfg == nil {
		cfg = config.DefaultConfig()
	}

	fileContent, _ := h.readFileFromPatchset(r.Context(), clID, psNum, filePath)
	targetCode := ai.ExtractTargetOnly(fileContent, line, endLine)

	sysPrompt := ai.DefaultSystemPrompt
	if cfg.Critique.AI.SystemPromptPath != "" {
		if b, err := os.ReadFile(cfg.Critique.AI.SystemPromptPath); err == nil && len(b) > 0 {
			sysPrompt = string(b)
		}
	}

	client, err := ai.NewClient(ai.Config{
		Endpoint:       cfg.Critique.AI.Endpoint,
		APIKey:         cfg.Critique.AI.APIKey,
		APIKeyEnv:      cfg.Critique.AI.APIKeyEnv,
		Model:          cfg.Critique.AI.Model,
		TimeoutSeconds: cfg.Critique.AI.TimeoutSeconds,
		Temperature:    cfg.Critique.AI.Temperature,
		TLS: httpclient.TLSOptions{
			Enabled:    cfg.Critique.AI.TLS.Enabled,
			CAFile:     cfg.Critique.AI.TLS.CAFile,
			ServerName: cfg.Critique.AI.TLS.ServerName,
		},
	})
	if err != nil {
		_ = views.AIHintPreviewCard(clID, filePath, endLine, "", targetCode, line, endLine, "", "", fmt.Sprintf("Failed to initialize AI client: %v", err)).Render(r.Context(), w)
		return
	}

	timeout := time.Duration(cfg.Critique.AI.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	hint, err := ai.GenerateWithAperture(ctx, client, fileContent, ai.HintRequest{
		SystemPrompt:    sysPrompt,
		FilePath:        filePath,
		LineStart:       line,
		LineEnd:         endLine,
		ReviewerComment: commentBody,
	}, cfg.Critique.AI.MaxContextLines)
	if err != nil {
		_ = views.AIHintPreviewCard(clID, filePath, endLine, "", targetCode, line, endLine, "", "", fmt.Sprintf("AI endpoint error: %v", err)).Render(r.Context(), w)
		return
	}

	_ = views.AIHintPreviewCard(
		clID,
		filePath,
		endLine,
		hint.Explanation,
		targetCode,
		hint.ReplacementStartLine,
		hint.ReplacementEndLine,
		hint.ReplacementCode,
		hint.ApertureUsed,
		"",
	).Render(r.Context(), w)
}

// HandleApplySuggestion handles POST /cl/{id}/comments/{comment_id}/apply-suggestion.
// It splices the suggested replacement code into the file, writes the new blob to CAS,
// updates the Merkle tree, creates Patchset N+1, and auto-resolves the comment thread.
func (h *CommentsHandler) HandleApplySuggestion(w http.ResponseWriter, r *http.Request) {
	commentID, err := parseCommentID(r)
	if err != nil {
		http.Error(w, "invalid comment_id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	activeReviewer := h.resolveReviewer(r)

	if h.db == nil {
		demoCommentsMu.Lock()
		var target *db.Comment
		for k, list := range demoCommentsStore {
			for i := range list {
				if list[i].ID == commentID {
					list[i].SuggestionAppliedPS = list[i].PatchsetNumber + 1
					list[i].Resolved = true
					c := list[i]
					target = &c
				}
			}
			demoCommentsStore[k] = list
		}
		demoCommentsMu.Unlock()
		if target == nil {
			http.Error(w, "comment not found", http.StatusNotFound)
			return
		}
		draftCount := h.countDrafts(r, target.CLID, activeReviewer)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		key := fmt.Sprintf("%d:%s:%d:%s", target.CLID, target.FilePath, target.EndLineNumber, target.Side)
		_ = views.CommentBox(target.CLID, target.PatchsetNumber, target.FilePath, target.LineNumber, target.EndLineNumber, target.Side, demoCommentsStore[key], draftCount).Render(h.renderContext(ctx), w)
		return
	}

	targetComment, err := h.db.GetComment(ctx, commentID)
	if err != nil {
		http.Error(w, "comment not found", http.StatusNotFound)
		return
	}
	if !targetComment.HasSuggestion || targetComment.SuggestionCode == "" {
		http.Error(w, "comment does not contain a code suggestion", http.StatusBadRequest)
		return
	}

	clID := targetComment.CLID
	cl, err := h.db.GetCL(ctx, clID)
	if err != nil {
		http.Error(w, "changelist not found", http.StatusNotFound)
		return
	}

	latestPS, err := h.db.GetLatestPatchsetNumber(ctx, clID)
	if err != nil || latestPS <= 0 {
		latestPS = targetComment.PatchsetNumber
	}

	var currentRootTree []byte
	if psObj, err := h.db.GetPatchset(ctx, clID, latestPS); err == nil && len(psObj.RootTreeHash) > 0 {
		currentRootTree = psObj.RootTreeHash
	} else {
		currentRootTree = cl.RootTreeHash
	}

	origContent, err := h.readFileFromTree(ctx, currentRootTree, targetComment.FilePath)
	if err != nil {
		log.Printf("error reading file %s for suggestion apply: %v", targetComment.FilePath, err)
	}

	startLine := targetComment.SuggestionStartLine
	if startLine <= 0 {
		startLine = targetComment.LineNumber
	}
	endLine := targetComment.SuggestionEndLine
	if endLine < startLine {
		endLine = targetComment.EndLineNumber
	}

	patchedContent, err := diff.ApplyLineRangeReplacement(origContent, startLine, endLine, targetComment.SuggestionCode)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to apply suggestion patch: %v", err), http.StatusBadRequest)
		return
	}

	newPSNum, err := h.db.GetNextPatchsetNumber(ctx, clID)
	if err != nil || newPSNum <= latestPS {
		newPSNum = latestPS + 1
	}

	if h.cas != nil {
		blobHash, _, blobSize, storagePath, err := h.cas.Put(bytes.NewReader([]byte(patchedContent)))
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to store patched blob in CAS: %v", err), http.StatusInternalServerError)
			return
		}
		_ = h.db.InsertBlob(ctx, blobHash, blobSize, storagePath)

		fetchTree := func(th []byte) ([]tree.Entry, error) {
			return h.db.GetTreeEntries(ctx, th)
		}
		saveTree := func(th []byte, entries []tree.Entry) error {
			return h.db.InsertTreeNodes(ctx, th, entries)
		}

		newRootHash, err := tree.UpdateFileEntry(currentRootTree, targetComment.FilePath, blobHash, uint64(blobSize), fetchTree, saveTree)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to update Merkle tree: %v", err), http.StatusInternalServerError)
			return
		}

		if err := h.db.CreatePatchset(ctx, clID, newPSNum, newRootHash); err != nil {
			http.Error(w, fmt.Sprintf("failed to create patchset %d: %v", newPSNum, err), http.StatusInternalServerError)
			return
		}
		_ = h.db.UpdateCLRootTreeHash(ctx, clID, newRootHash)
	}

	if err := h.db.MarkSuggestionApplied(ctx, commentID, newPSNum); err != nil {
		http.Error(w, fmt.Sprintf("failed to mark suggestion applied: %v", err), http.StatusInternalServerError)
		return
	}

	anchorLine := targetComment.EndLineNumber
	if anchorLine <= 0 {
		anchorLine = targetComment.LineNumber
	}
	rawComments, _ := h.db.ListCommentsForLine(ctx, clID, targetComment.FilePath, anchorLine, targetComment.Side)
	comments := filterVisibleComments(rawComments, activeReviewer)
	draftCount := h.countDrafts(r, clID, activeReviewer)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.CommentBox(clID, newPSNum, targetComment.FilePath, targetComment.LineNumber, anchorLine, targetComment.Side, comments, draftCount).Render(h.renderContext(ctx), w)
}

// HandleRejectSuggestion handles POST /cl/{id}/comments/{comment_id}/reject-suggestion.
func (h *CommentsHandler) HandleRejectSuggestion(w http.ResponseWriter, r *http.Request) {
	commentID, err := parseCommentID(r)
	if err != nil {
		http.Error(w, "invalid comment_id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	activeReviewer := h.resolveReviewer(r)

	if h.db == nil {
		demoCommentsMu.Lock()
		var target *db.Comment
		for k, list := range demoCommentsStore {
			for i := range list {
				if list[i].ID == commentID {
					list[i].SuggestionAppliedPS = -1
					c := list[i]
					target = &c
				}
			}
			demoCommentsStore[k] = list
		}
		demoCommentsMu.Unlock()
		if target == nil {
			http.Error(w, "comment not found", http.StatusNotFound)
			return
		}
		draftCount := h.countDrafts(r, target.CLID, activeReviewer)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		key := fmt.Sprintf("%d:%s:%d:%s", target.CLID, target.FilePath, target.EndLineNumber, target.Side)
		_ = views.CommentBox(target.CLID, target.PatchsetNumber, target.FilePath, target.LineNumber, target.EndLineNumber, target.Side, demoCommentsStore[key], draftCount).Render(h.renderContext(ctx), w)
		return
	}

	targetComment, err := h.db.GetComment(ctx, commentID)
	if err != nil {
		http.Error(w, "comment not found", http.StatusNotFound)
		return
	}

	if err := h.db.RejectCommentSuggestion(ctx, commentID); err != nil {
		http.Error(w, "failed to reject suggestion", http.StatusInternalServerError)
		return
	}

	anchorLine := targetComment.EndLineNumber
	if anchorLine <= 0 {
		anchorLine = targetComment.LineNumber
	}
	rawComments, _ := h.db.ListCommentsForLine(ctx, targetComment.CLID, targetComment.FilePath, anchorLine, targetComment.Side)
	comments := filterVisibleComments(rawComments, activeReviewer)
	draftCount := h.countDrafts(r, targetComment.CLID, activeReviewer)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = views.CommentBox(targetComment.CLID, targetComment.PatchsetNumber, targetComment.FilePath, targetComment.LineNumber, anchorLine, targetComment.Side, comments, draftCount).Render(h.renderContext(ctx), w)
}

func (h *CommentsHandler) readFileFromPatchset(ctx context.Context, clID int64, psNum int, filePath string) (string, error) {
	if h.db == nil || h.cas == nil {
		return "", nil
	}
	var rootHash []byte
	if ps, err := h.db.GetPatchset(ctx, clID, psNum); err == nil && len(ps.RootTreeHash) > 0 {
		rootHash = ps.RootTreeHash
	} else if cl, err := h.db.GetCL(ctx, clID); err == nil {
		rootHash = cl.RootTreeHash
	}
	return h.readFileFromTree(ctx, rootHash, filePath)
}

func (h *CommentsHandler) readFileFromTree(ctx context.Context, rootHash []byte, filePath string) (string, error) {
	if h.db == nil || h.cas == nil || len(rootHash) == 0 {
		return "", nil
	}
	entry, err := tree.ResolvePath(rootHash, filePath, func(th []byte) ([]tree.Entry, error) {
		return h.db.GetTreeEntries(ctx, th)
	})
	if err != nil || entry == nil || len(entry.BlobSHA256) == 0 {
		return "", err
	}
	f, _, err := h.cas.OpenBlob(entry.BlobSHA256)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	return string(b), err
}

func parseCommentID(r *http.Request) (int64, error) {
	commentIDStr := r.PathValue("comment_id")
	if commentIDStr == "" {
		parts := strings.Split(r.URL.Path, "/")
		for i, part := range parts {
			if part == "comments" && i+1 < len(parts) {
				commentIDStr = parts[i+1]
				break
			}
		}
	}
	return strconv.ParseInt(commentIDStr, 10, 64)
}

