package handlers

import (
	"bytes"
	"context"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"citc-toolchain/internal/db"
	"citc-toolchain/internal/web/diff"
	"citc-toolchain/internal/web/views"
	"citc-toolchain/pkg/cas"
)

func TestRawBlobHandler(t *testing.T) {
	tempDir := t.TempDir()
	storage, err := cas.NewStorage(tempDir)
	if err != nil {
		t.Fatalf("failed to create CAS storage: %v", err)
	}

	content := []byte("hello world from critique raw blob test")
	hashBytes, hashHex, _, _, err := storage.Put(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("failed to put blob in CAS: %v", err)
	}

	rawHandler := NewRawHandler(nil, storage)

	// 1. Success case
	req := httptest.NewRequest("GET", "/cl/1/raw?hash="+hashHex, nil)
	rec := httptest.NewRecorder()
	rawHandler.ServeRawBlob(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
	if rec.Body.String() != string(content) {
		t.Errorf("expected body %q, got %q", string(content), rec.Body.String())
	}

	// 2. Missing hash
	req = httptest.NewRequest("GET", "/cl/1/raw", nil)
	rec = httptest.NewRecorder()
	rawHandler.ServeRawBlob(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for missing hash, got %d", rec.Code)
	}

	// 3. Not found hash
	missingHash := hex.EncodeToString(make([]byte, 32))
	req = httptest.NewRequest("GET", "/cl/1/raw?hash="+missingHash, nil)
	rec = httptest.NewRecorder()
	rawHandler.ServeRawBlob(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for missing blob, got %d", rec.Code)
	}

	_ = hashBytes
}

func TestViewsRendering(t *testing.T) {
	ctx := context.Background()

	// 1. Test CommentBox View Rendering
	comments := []db.Comment{
		{
			ID:             1,
			CLID:           101,
			PatchsetNumber: 1,
			FilePath:       "main.go",
			LineNumber:     42,
			Side:           "modified",
			Author:         "matatan",
			Body:           "Please consider refactoring this function.",
			Resolved:       false,
			CreatedAt:      time.Now(),
		},
	}

	// 1a. Test CommentBox View Rendering (Single Line)
	var commentBuf bytes.Buffer
	commentComp := views.CommentBox(101, 1, "main.go", 42, 42, "modified", comments)
	if err := commentComp.Render(ctx, &commentBuf); err != nil {
		t.Fatalf("CommentBox rendering failed: %v", err)
	}

	commentHTML := commentBuf.String()
	if !strings.Contains(commentHTML, "main.go : Line 42 (modified)") {
		t.Errorf("expected header with file and single line in CommentBox, got %s", commentHTML)
	}
	if !strings.Contains(commentHTML, "Please consider refactoring this function.") {
		t.Errorf("expected comment body in CommentBox")
	}
	if !strings.Contains(commentHTML, "Unresolved") {
		t.Errorf("expected unresolved badge in CommentBox")
	}

	// 1b. Test CommentBox View Rendering (Multi-Line Code Block)
	var multiCommentBuf bytes.Buffer
	multiCommentComp := views.CommentBox(101, 1, "main.go", 42, 48, "modified", comments)
	if err := multiCommentComp.Render(ctx, &multiCommentBuf); err != nil {
		t.Fatalf("CommentBox multi-line rendering failed: %v", err)
	}

	multiHTML := multiCommentBuf.String()
	if !strings.Contains(multiHTML, "main.go : Lines 42–48 (modified)") {
		t.Errorf("expected header with multi-line range in CommentBox, got %s", multiHTML)
	}
	if !strings.Contains(multiHTML, `value="48"`) {
		t.Errorf("expected end_line_number input value 48 in CommentBox")
	}

	// 2. Test DiffView View Rendering
	base := []byte("package main\nfunc Add(a, b int) int {\nreturn a + b\n}\n")
	mod := []byte("package main\nfunc Add(a, b int) int {\n// added comment\nreturn a + b\n}\n")

	detail, err := diff.ComputeSideBySideDiff("math.go", base, mod, "M")
	if err != nil {
		t.Fatalf("ComputeSideBySideDiff failed: %v", err)
	}

	commentsMap := map[int][]db.Comment{
		3: comments,
	}

	var diffBuf bytes.Buffer
	diffComp := views.DiffView(101, 1, 0, detail, commentsMap)
	if err := diffComp.Render(ctx, &diffBuf); err != nil {
		t.Fatalf("DiffView rendering failed: %v", err)
	}

	diffHTML := diffBuf.String()
	if !strings.Contains(diffHTML, "math.go") {
		t.Errorf("expected file path in DiffView")
	}
	if !strings.Contains(diffHTML, "diff-line-num") {
		t.Errorf("expected diff line numbers in DiffView")
	}
	if !strings.Contains(diffHTML, "hx-get=\"/cl/101/comment-box?file=math.go") {
		t.Errorf("expected HTMX comment-box trigger in DiffView")
	}

	// 3. Test CLDetail View Rendering
	cl := &db.Changelist{
		CLID:         101,
		Author:       "alice@corp",
		Description:  "Implement fast arithmetic routines",
		Status:       "shelved",
		CommittedAt:  time.Now(),
		RootTreeHash: []byte{1, 2, 3},
	}
	patchsets := []db.Patchset{
		{PatchsetID: 1, CLID: 101, PatchsetNumber: 1, RootTreeHash: []byte{1, 2, 3}},
	}
	files := []diff.FileDiffSummary{
		{Path: "math.go", Type: "M", Additions: 1, Deletions: 0, UnresolvedComments: 1},
	}

	clData := views.CLDetailData{
		CL:                 cl,
		Patchsets:          patchsets,
		CurrentPatchset:    1,
		BasePatchset:       0,
		Files:              files,
		SelectedFilePath:   "math.go",
		SelectedFileDiff:   detail,
		CommentsByLine:     commentsMap,
		Approvals:          []db.Approval{{CLID: 101, Reviewer: "bob", Verdict: "lgtm"}},
		UnresolvedComments: 1,
	}

	var clDetailBuf bytes.Buffer
	clDetailComp := views.CLDetail(clData)
	if err := clDetailComp.Render(ctx, &clDetailBuf); err != nil {
		t.Fatalf("CLDetail rendering failed: %v", err)
	}

	clDetailHTML := clDetailBuf.String()
	if !strings.Contains(clDetailHTML, "CL 101") {
		t.Errorf("expected CL ID in CLDetail")
	}
	if !strings.Contains(clDetailHTML, "LGTM / Approve") {
		t.Errorf("expected LGTM / Approve button in CLDetail")
	}
	if !strings.Contains(clDetailHTML, "Patchset 1 vs Base CL") {
		t.Errorf("expected patchset selector dropdown in CLDetail")
	}
	if !strings.Contains(clDetailHTML, "1 Unresolved Comment") {
		t.Errorf("expected unresolved comment badge in CLDetail")
	}
}

func TestCommentHandlersMultiLine(t *testing.T) {
	commentsHandler := NewCommentsHandler(nil)

	// 1. Test GET /cl/101/comment-box with line=10&end_line=15
	req := httptest.NewRequest("GET", "/cl/101/comment-box?file=service.go&line=10&end_line=15&side=modified&ps=1", nil)
	rec := httptest.NewRecorder()
	commentsHandler.HandleGetCommentBox(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleGetCommentBox failed with status %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "service.go : Lines 10–15 (modified)") {
		t.Errorf("expected multi-line header 'service.go : Lines 10–15 (modified)', got: %s", body)
	}
	if !strings.Contains(body, `name="end_line_number" value="15"`) {
		t.Errorf("expected hidden end_line_number input with value 15, got: %s", body)
	}

	// 2. Test POST /cl/101/comments creating a multi-line comment
	form := strings.NewReader("cl_id=101&patchset_number=1&file_path=service.go&line_number=10&end_line_number=15&side=modified&author=matatan&body=Please+extract+this+block+into+a+helper+function.")
	postReq := httptest.NewRequest("POST", "/cl/101/comments", form)
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postRec := httptest.NewRecorder()
	commentsHandler.HandleCreateComment(postRec, postReq)

	if postRec.Code != http.StatusOK {
		t.Fatalf("HandleCreateComment failed with status %d: %s", postRec.Code, postRec.Body.String())
	}
	postBody := postRec.Body.String()
	if !strings.Contains(postBody, "service.go : Lines 10–15 (modified)") {
		t.Errorf("expected multi-line header in rendered comment box, got: %s", postBody)
	}
	if !strings.Contains(postBody, "Please extract this block into a helper function.") {
		t.Errorf("expected comment body in rendered comment box, got: %s", postBody)
	}
}

func init() {
	// Ensure working directory is repo root if running from submodule
	if _, err := os.Stat("internal/web/views"); os.IsNotExist(err) {
		_ = os.Chdir(filepath.Join("..", ".."))
	}
}

