package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"citc-toolchain/internal/db"
	"citc-toolchain/internal/web/views"
)

var (
	demoCommentsMu    sync.RWMutex
	demoCommentsStore = make(map[string][]db.Comment)
	nextCommentID     int64 = 100
)

// CommentsHandler handles HTMX endpoints for inline code reviews.
type CommentsHandler struct {
	db *db.DB
}

// NewCommentsHandler initializes a new CommentsHandler.
func NewCommentsHandler(database *db.DB) *CommentsHandler {
	return &CommentsHandler{db: database}
}

// HandleGetCommentBox renders the inline comment thread and input form for a file line.
// GET /cl/{id}/comment-box?file={path}&line={line}&side={side}&ps={ps}
func (h *CommentsHandler) HandleGetCommentBox(w http.ResponseWriter, r *http.Request) {
	clIDStr := r.PathValue("id")
	if clIDStr == "" {
		// Fallback for paths without PathValue
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

	var comments []db.Comment
	if h.db != nil {
		var err error
		comments, err = h.db.ListCommentsForLine(r.Context(), clID, filePath, endLine, side)
		if err != nil {
			log.Printf("error listing comments for line: %v", err)
		}
	} else {
		demoCommentsMu.RLock()
		key := fmt.Sprintf("%d:%s:%d:%s", clID, filePath, endLine, side)
		comments = demoCommentsStore[key]
		if len(comments) == 0 && endLine != line {
			keyStart := fmt.Sprintf("%d:%s:%d:%s", clID, filePath, line, side)
			comments = demoCommentsStore[keyStart]
		}
		demoCommentsMu.RUnlock()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.CommentBox(clID, psNum, filePath, line, endLine, side, comments)
	_ = component.Render(r.Context(), w)
}

// HandleCreateComment creates a new inline comment on a specific code line or multi-line range.
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
		author = "reviewer"
	}

	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		http.Error(w, "comment body cannot be empty", http.StatusBadRequest)
		return
	}

	resolved := r.FormValue("resolved") == "true"
	var comments []db.Comment

	if h.db != nil {
		newComment, err := h.db.CreateComment(r.Context(), clID, psNum, filePath, line, endLine, side, author, body, nil)
		if err != nil {
			log.Printf("failed to insert comment: %v", err)
			http.Error(w, "failed to record comment", http.StatusInternalServerError)
			return
		}

		if resolved {
			_ = h.db.ResolveComment(r.Context(), newComment.ID, true)
		}

		comments, err = h.db.ListCommentsForLine(r.Context(), clID, filePath, endLine, side)
		if err != nil {
			log.Printf("error fetching updated comments: %v", err)
		}
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
			CreatedAt:      time.Now(),
		}
		key := fmt.Sprintf("%d:%s:%d:%s", clID, filePath, endLine, side)
		demoCommentsStore[key] = append(demoCommentsStore[key], c)
		comments = demoCommentsStore[key]
		demoCommentsMu.Unlock()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.CommentBox(clID, psNum, filePath, line, endLine, side, comments)
	_ = component.Render(r.Context(), w)
}

// HandleResolveComment toggles the resolved status of an inline comment.
// POST /cl/{id}/comments/{comment_id}/resolve
func (h *CommentsHandler) HandleResolveComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := r.PathValue("comment_id")
	if commentIDStr == "" {
		// Fallback for paths without PathValue
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

		comments, err := h.db.ListCommentsForLine(r.Context(), comment.CLID, comment.FilePath, anchorLine, comment.Side)
		if err != nil {
			log.Printf("error querying comments for resolved line: %v", err)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		component := views.CommentBox(comment.CLID, comment.PatchsetNumber, comment.FilePath, comment.LineNumber, anchorLine, comment.Side, comments)
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
		comments = demoCommentsStore[matchedKey]
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

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	component := views.CommentBox(matched.CLID, matched.PatchsetNumber, matched.FilePath, matched.LineNumber, anchorLine, matched.Side, comments)
	_ = component.Render(r.Context(), w)
}
