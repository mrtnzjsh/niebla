package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"niebla/internal/db"
	"niebla/internal/web/auth"
	"niebla/internal/web/views"
)

var (
	demoCommentsMu    sync.RWMutex
	demoCommentsStore       = make(map[string][]db.Comment)
	nextCommentID     int64 = 100
)

// CommentsHandler handles HTMX endpoints for inline code reviews and drafts.
type CommentsHandler struct {
	db           *db.DB
	authResolver auth.UserIdentityResolver
}

// NewCommentsHandler initializes a new CommentsHandler with optional auth resolver.
func NewCommentsHandler(database *db.DB, resolvers ...auth.UserIdentityResolver) *CommentsHandler {
	var resolver auth.UserIdentityResolver
	if len(resolvers) > 0 && resolvers[0] != nil {
		resolver = resolvers[0]
	} else {
		resolver = &auth.LocalResolver{DefaultUser: auth.CurrentOSUser()}
	}
	return &CommentsHandler{
		db:           database,
		authResolver: resolver,
	}
}

func (h *CommentsHandler) resolveReviewer(r *http.Request) string {
	if h.authResolver != nil {
		return h.authResolver.ResolveUser(r)
	}
	if r != nil {
		if q := r.URL.Query().Get("reviewer"); q != "" {
			return strings.TrimSpace(q)
		}
		if a := r.FormValue("author"); a != "" {
			return strings.TrimSpace(a)
		}
	}
	return auth.CurrentOSUser()
}

func filterVisibleComments(comments []db.Comment, activeReviewer string) []db.Comment {
	var visible []db.Comment
	for _, c := range comments {
		// Visible if already published OR authored as draft by active reviewer
		if !c.IsDraft || c.Author == activeReviewer {
			visible = append(visible, c)
		}
	}
	return visible
}

func (h *CommentsHandler) countDrafts(r *http.Request, clID int64, author string) int {
	if h.db != nil {
		count, err := h.db.CountDraftComments(r.Context(), clID, author)
		if err == nil {
			return count
		}
		return 0
	}
	demoCommentsMu.RLock()
	defer demoCommentsMu.RUnlock()
	count := 0
	for _, list := range demoCommentsStore {
		for _, c := range list {
			if c.CLID == clID && c.Author == author && c.IsDraft {
				count++
			}
		}
	}
	return count
}

// HandleGetCommentBox renders the inline comment thread and input form for a file line.
// GET /cl/{id}/comment-box?file={path}&line={line}&side={side}&ps={ps}
func (h *CommentsHandler) HandleGetCommentBox(w http.ResponseWriter, r *http.Request) {
	clIDStr := r.PathValue("id")
	if clIDStr == "" {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) >= 3 {
			clIDStr = parts[2]
		}
	}

	clID, err := strconv.ParseInt(clIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid cl_id", http.StatusBadRequest)
		return
	}

	filePath := r.URL.Query().Get("file")
	lineStr := r.URL.Query().Get("line")
	side := r.URL.Query().Get("side")
	if side == "" {
		side = "modified"
	}
	psStr := r.URL.Query().Get("ps")
	psNum, _ := strconv.Atoi(psStr)
	if psNum <= 0 {
		psNum = 1
	}

	line, err := strconv.Atoi(lineStr)
	if err != nil || line <= 0 {
		http.Error(w, "invalid line number", http.StatusBadRequest)
		return
	}

	endLineStr := r.URL.Query().Get("end_line")
	endLine, _ := strconv.Atoi(endLineStr)
	if endLine <= 0 {
		endLine = line
	}
	if endLine < line {
		line, endLine = endLine, line
	}

	activeReviewer := h.resolveReviewer(r)
	var comments []db.Comment

	if h.db != nil {
		rawComments, err := h.db.ListCommentsForLine(r.Context(), clID, filePath, endLine, side)
		if err != nil {
			log.Printf("error listing comments for line: %v", err)
		}
		comments = filterVisibleComments(rawComments, activeReviewer)
	} else {
		demoCommentsMu.RLock()
		key := fmt.Sprintf("%d:%s:%d:%s", clID, filePath, endLine, side)
		raw := demoCommentsStore[key]
		if len(raw) == 0 && endLine != line {
			keyStart := fmt.Sprintf("%d:%s:%d:%s", clID, filePath, line, side)
			raw = demoCommentsStore[keyStart]
		}
		comments = filterVisibleComments(raw, activeReviewer)
		demoCommentsMu.RUnlock()
	}

	draftCount := h.countDrafts(r, clID, activeReviewer)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.CommentBox(clID, psNum, filePath, line, endLine, side, comments, draftCount)
	_ = component.Render(r.Context(), w)
}

// HandleCreateComment creates a new inline draft comment on a specific code line or multi-line range.
// POST /cl/{id}/comments
func (h *CommentsHandler) HandleCreateComment(w http.ResponseWriter, r *http.Request) {
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
	line, err := strconv.Atoi(r.FormValue("line_number"))
	if err != nil || line <= 0 {
		http.Error(w, "invalid line_number", http.StatusBadRequest)
		return
	}

	endLine, _ := strconv.Atoi(r.FormValue("end_line_number"))
	if endLine <= 0 {
		endLine = line
	}
	if endLine < line {
		line, endLine = endLine, line
	}

	side := r.FormValue("side")
	if side == "" {
		side = "modified"
	}

	psNum, _ := strconv.Atoi(r.FormValue("patchset_number"))
	if psNum <= 0 {
		psNum = 1
	}

	author := strings.TrimSpace(r.FormValue("author"))
	if author == "" {
		author = h.resolveReviewer(r)
	}

	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		http.Error(w, "comment body cannot be empty", http.StatusBadRequest)
		return
	}

	resolved := r.FormValue("resolved") == "true"
	var comments []db.Comment

	if h.db != nil {
		// New inline comments are saved as unposted drafts (IsDraft = true)
		newComment, err := h.db.CreateComment(r.Context(), clID, psNum, filePath, line, endLine, side, author, body, nil, true)
		if err != nil {
			log.Printf("failed to insert draft comment: %v", err)
			http.Error(w, "failed to record draft comment", http.StatusInternalServerError)
			return
		}

		if resolved {
			_ = h.db.ResolveComment(r.Context(), newComment.ID, true)
		}

		rawComments, err := h.db.ListCommentsForLine(r.Context(), clID, filePath, endLine, side)
		if err != nil {
			log.Printf("error fetching updated comments: %v", err)
		}
		comments = filterVisibleComments(rawComments, author)
	} else {
		demoCommentsMu.Lock()
		nextCommentID++
		c := db.Comment{
			ID:             nextCommentID,
			CLID:           clID,
			PatchsetNumber: psNum,
			FilePath:       filePath,
			LineNumber:     line,
			EndLineNumber:  endLine,
			Side:           side,
			Author:         author,
			Body:           body,
			Resolved:       resolved,
			IsDraft:        true,
			CreatedAt:      time.Now(),
		}
		key := fmt.Sprintf("%d:%s:%d:%s", clID, filePath, endLine, side)
		demoCommentsStore[key] = append(demoCommentsStore[key], c)
		comments = filterVisibleComments(demoCommentsStore[key], author)
		demoCommentsMu.Unlock()
	}

	draftCount := h.countDrafts(r, clID, author)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.CommentBox(clID, psNum, filePath, line, endLine, side, comments, draftCount)
	_ = component.Render(r.Context(), w)
}

// HandleDiscardComment deletes an unposted draft comment.
// POST /cl/{id}/comments/{comment_id}/discard
func (h *CommentsHandler) HandleDiscardComment(w http.ResponseWriter, r *http.Request) {
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

	commentID, err := strconv.ParseInt(commentIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid comment_id", http.StatusBadRequest)
		return
	}

	author := strings.TrimSpace(r.FormValue("author"))
	if author == "" {
		author = h.resolveReviewer(r)
	}

	var clID int64
	var patchsetNumber int
	var filePath string
	var line int
	var endLine int
	var side string
	var comments []db.Comment

	if h.db != nil {
		targetComment, err := h.db.GetComment(r.Context(), commentID)
		if err != nil {
			http.Error(w, "comment not found", http.StatusNotFound)
			return
		}
		clID = targetComment.CLID
		patchsetNumber = targetComment.PatchsetNumber
		filePath = targetComment.FilePath
		line = targetComment.LineNumber
		endLine = targetComment.EndLineNumber
		if endLine <= 0 {
			endLine = line
		}
		side = targetComment.Side

		if err := h.db.DiscardDraftComment(r.Context(), commentID, author); err != nil {
			log.Printf("failed to discard draft comment %d: %v", commentID, err)
			http.Error(w, "failed to discard draft", http.StatusInternalServerError)
			return
		}

		rawComments, err := h.db.ListCommentsForLine(r.Context(), clID, filePath, endLine, side)
		if err != nil {
			log.Printf("error querying comments after discard: %v", err)
		}
		comments = filterVisibleComments(rawComments, author)
	} else {
		demoCommentsMu.Lock()
		var matchedKey string
		for k, list := range demoCommentsStore {
			for i := range list {
				if list[i].ID == commentID && list[i].IsDraft && list[i].Author == author {
					clID = list[i].CLID
					patchsetNumber = list[i].PatchsetNumber
					filePath = list[i].FilePath
					line = list[i].LineNumber
					endLine = list[i].EndLineNumber
					if endLine <= 0 {
						endLine = line
					}
					side = list[i].Side
					matchedKey = k
					demoCommentsStore[k] = append(list[:i], list[i+1:]...)
					break
				}
			}
			if matchedKey != "" {
				break
			}
		}
		if matchedKey != "" {
			comments = filterVisibleComments(demoCommentsStore[matchedKey], author)
		}
		demoCommentsMu.Unlock()

		if matchedKey == "" {
			http.Error(w, "draft comment not found", http.StatusNotFound)
			return
		}
	}

	draftCount := h.countDrafts(r, clID, author)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.CommentBox(clID, patchsetNumber, filePath, line, endLine, side, comments, draftCount)
	_ = component.Render(r.Context(), w)
}

// HandleResolveComment toggles the resolved status of an inline comment.
// POST /cl/{id}/comments/{comment_id}/resolve
func (h *CommentsHandler) HandleResolveComment(w http.ResponseWriter, r *http.Request) {
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

	commentID, err := strconv.ParseInt(commentIDStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid comment_id", http.StatusBadRequest)
		return
	}

	resolved := r.URL.Query().Get("resolved") == "true"
	author := strings.TrimSpace(r.FormValue("author"))
	if author == "" {
		author = h.resolveReviewer(r)
	}

	if h.db != nil {
		if err := h.db.ResolveComment(r.Context(), commentID, resolved); err != nil {
			log.Printf("failed to resolve comment %d: %v", commentID, err)
			http.Error(w, "failed to update comment state", http.StatusInternalServerError)
			return
		}

		comment, err := h.db.GetComment(r.Context(), commentID)
		if err != nil {
			http.Error(w, "comment not found", http.StatusNotFound)
			return
		}

		anchorLine := comment.EndLineNumber
		if anchorLine <= 0 {
			anchorLine = comment.LineNumber
		}

		rawComments, err := h.db.ListCommentsForLine(r.Context(), comment.CLID, comment.FilePath, anchorLine, comment.Side)
		if err != nil {
			log.Printf("error querying comments for resolved line: %v", err)
		}
		comments := filterVisibleComments(rawComments, author)
		draftCount := h.countDrafts(r, comment.CLID, author)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		component := views.CommentBox(comment.CLID, comment.PatchsetNumber, comment.FilePath, comment.LineNumber, anchorLine, comment.Side, comments, draftCount)
		_ = component.Render(r.Context(), w)
		return
	}

	demoCommentsMu.Lock()
	var matched *db.Comment
	var matchedKey string
	for k, list := range demoCommentsStore {
		for i := range list {
			if list[i].ID == commentID {
				list[i].Resolved = resolved
				matched = &list[i]
				matchedKey = k
				break
			}
		}
	}
	var comments []db.Comment
	if matched != nil {
		comments = filterVisibleComments(demoCommentsStore[matchedKey], author)
	}
	demoCommentsMu.Unlock()

	if matched == nil {
		http.Error(w, "comment not found", http.StatusNotFound)
		return
	}

	anchorLine := matched.EndLineNumber
	if anchorLine <= 0 {
		anchorLine = matched.LineNumber
	}

	draftCount := h.countDrafts(r, matched.CLID, author)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.CommentBox(matched.CLID, matched.PatchsetNumber, matched.FilePath, matched.LineNumber, anchorLine, matched.Side, comments, draftCount)
	_ = component.Render(r.Context(), w)
}
