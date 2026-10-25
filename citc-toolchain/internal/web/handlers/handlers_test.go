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
			Author:         "alice",
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
		ActiveReviewer:     "alice",
		ReviewedFilesCount: 1,
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
	if !strings.Contains(clDetailHTML, "1 of 1 reviewed by alice") {
		t.Errorf("expected review progress in CLDetail, got %s", clDetailHTML)
	}
	if !strings.Contains(clDetailHTML, "Review") {
		t.Errorf("expected Review column header in CLDetail")
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

	// 2. Test POST /cl/101/comments creating a multi-line draft comment
	form := strings.NewReader("cl_id=101&patchset_number=1&file_path=service.go&line_number=10&end_line_number=15&side=modified&author=alice&body=Please+extract+this+block+into+a+helper+function.")
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
	if !strings.Contains(postBody, "Save Draft") {
		t.Errorf("expected 'Save Draft' submit button, got: %s", postBody)
	}
	if !strings.Contains(postBody, "Draft") {
		t.Errorf("expected 'Draft' badge in rendered comment box, got: %s", postBody)
	}
	if !strings.Contains(postBody, "Discard") {
		t.Errorf("expected 'Discard' button in rendered comment box, got: %s", postBody)
	}
}

func TestDraftCommentsAndReplyWorkflow(t *testing.T) {
	ResetDemoStoresForTest()
	clHandler := NewCLHandler(nil, nil)
	commentsHandler := NewCommentsHandler(nil)

	// 1. Alice creates draft comment 1 on math.go line 10
	form1 := strings.NewReader("cl_id=1&patchset_number=1&file_path=internal/web/diff/engine.go&line_number=10&side=modified&author=alice&body=First+draft+note")
	req1 := httptest.NewRequest("POST", "/cl/1/comments", form1)
	req1.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec1 := httptest.NewRecorder()
	commentsHandler.HandleCreateComment(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("Create draft 1 failed with status %d: %s", rec1.Code, rec1.Body.String())
	}
	body1 := rec1.Body.String()
	if !strings.Contains(body1, "First draft note") {
		t.Errorf("expected draft body in comment box")
	}
	if !strings.Contains(body1, "Draft") {
		t.Errorf("expected Draft badge in comment box")
	}

	// 2. Alice creates draft comment 2 on line 20
	form2 := strings.NewReader("cl_id=1&patchset_number=1&file_path=internal/web/diff/engine.go&line_number=20&side=modified&author=alice&body=Second+draft+note")
	req2 := httptest.NewRequest("POST", "/cl/1/comments", form2)
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec2 := httptest.NewRecorder()
	commentsHandler.HandleCreateComment(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("Create draft 2 failed with status %d", rec2.Code)
	}
	body2 := rec2.Body.String()
	if !strings.Contains(body2, "Second draft note") {
		t.Errorf("expected second draft body in comment box")
	}

	// 2b. Alice creates draft comment 3, then discards it
	form3 := strings.NewReader("cl_id=1&patchset_number=1&file_path=internal/web/diff/engine.go&line_number=30&side=modified&author=alice&body=Temporary+draft+note")
	req3 := httptest.NewRequest("POST", "/cl/1/comments", form3)
	req3.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec3 := httptest.NewRecorder()
	commentsHandler.HandleCreateComment(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("Create draft 3 failed: %d", rec3.Code)
	}

	// Discard draft 3
	reqDiscard := httptest.NewRequest("POST", "/cl/1/comments/103/discard?author=alice", nil)
	reqDiscard.SetPathValue("comment_id", "103")
	recDiscard := httptest.NewRecorder()
	commentsHandler.HandleDiscardComment(recDiscard, reqDiscard)
	if recDiscard.Code != http.StatusOK {
		t.Fatalf("Discard draft failed with status %d: %s", recDiscard.Code, recDiscard.Body.String())
	}
	if strings.Contains(recDiscard.Body.String(), "Temporary draft note") {
		t.Errorf("discarded draft should not appear in response")
	}

	// 3. Bob visits CL 1 page -> should have 0 drafts for bob (per-reviewer draft isolation)
	reqBob := httptest.NewRequest("GET", "/cl/1?reviewer=bob", nil)
	recBob := httptest.NewRecorder()
	clHandler.HandleCLView(recBob, reqBob)
	if recBob.Code != http.StatusOK {
		t.Fatalf("HandleCLView as bob failed with status %d", recBob.Code)
	}
	bobView := recBob.Body.String()
	if strings.Contains(bobView, "First draft note") {
		t.Errorf("Bob should NOT see Alice's unposted draft comments")
	}

	// 4. Alice posts comments & reply with LGTM verdict
	replyForm := strings.NewReader("author=alice&message=LGTM+overall!+Addressed+comments.&verdict=lgtm")
	postReply := httptest.NewRequest("POST", "/cl/1/reply", replyForm)
	postReply.SetPathValue("id", "1")
	postReply.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recReply := httptest.NewRecorder()
	clHandler.HandlePublishReview(recReply, postReply)

	if recReply.Code != http.StatusSeeOther {
		t.Fatalf("HandlePublishReview returned status %d, expected 303 redirect", recReply.Code)
	}

	// 5. Alice visits CL 1 after publishing -> drafts are now published comments
	reqAliceAfter := httptest.NewRequest("GET", "/cl/1?reviewer=alice", nil)
	recAliceAfter := httptest.NewRecorder()
	clHandler.HandleCLView(recAliceAfter, reqAliceAfter)
	aliceViewAfter := recAliceAfter.Body.String()
	if !strings.Contains(aliceViewAfter, "First draft note") {
		t.Errorf("expected published comment to be visible on CL view after reply")
	}
	if !strings.Contains(aliceViewAfter, "LGTM / Approve") {
		t.Errorf("expected approval buttons on CL view")
	}
}

func TestFileReviewEndpoints(t *testing.T) {
	ResetDemoStoresForTest()
	handler := NewCLHandler(nil, nil)

	// 1. Visit CL page as alice (auto-selects first file and marks it reviewed for alice)
	reqAlice := httptest.NewRequest("GET", "/cl/1?reviewer=alice", nil)
	recAlice := httptest.NewRecorder()
	handler.HandleCLView(recAlice, reqAlice)

	if recAlice.Code != http.StatusOK {
		t.Fatalf("HandleCLView as alice returned status %d", recAlice.Code)
	}
	bodyAlice := recAlice.Body.String()
	if !strings.Contains(bodyAlice, "1 of 4 reviewed by alice") {
		t.Errorf("expected '1 of 4 reviewed by alice', got: %s", bodyAlice)
	}

	// 2. Visit CL page as bob without viewing alice's file -> should be 0 of 4 reviewed by bob
	reqBob := httptest.NewRequest("GET", "/cl/1?reviewer=bob&file=cmd/critique/main.go", nil)
	recBob := httptest.NewRecorder()
	handler.HandleCLView(recBob, reqBob)

	if recBob.Code != http.StatusOK {
		t.Fatalf("HandleCLView as bob returned status %d", recBob.Code)
	}
	bodyBob := recBob.Body.String()
	if !strings.Contains(bodyBob, "1 of 4 reviewed by bob") {
		t.Errorf("expected '1 of 4 reviewed by bob', got: %s", bodyBob)
	}

	// 3. Mark another file as reviewed for bob via POST /cl/1/file-review
	postBob := httptest.NewRequest("POST", "/cl/1/file-review?file=assets/css/input.css&reviewed=true&reviewer=bob", nil)
	recPostBob := httptest.NewRecorder()
	handler.HandleFileReview(recPostBob, postBob)

	if recPostBob.Code != http.StatusOK {
		t.Fatalf("HandleFileReview returned status %d", recPostBob.Code)
	}
	postBadge := recPostBob.Body.String()
	if !strings.Contains(postBadge, "reviewed") {
		t.Errorf("expected reviewed badge in response, got: %s", postBadge)
	}

	// 4. Toggle file back to not reviewed for bob
	postUnreview := httptest.NewRequest("POST", "/cl/1/file-review?file=assets/css/input.css&reviewed=false&reviewer=bob", nil)
	recUnreview := httptest.NewRecorder()
	handler.HandleFileReview(recUnreview, postUnreview)

	if recUnreview.Code != http.StatusOK {
		t.Fatalf("HandleFileReview unreview returned status %d", recUnreview.Code)
	}
	unreviewBadge := recUnreview.Body.String()
	if !strings.Contains(unreviewBadge, "not reviewed") {
		t.Errorf("expected not reviewed badge in response, got: %s", unreviewBadge)
	}
}

func init() {
	// Ensure working directory is repo root if running from submodule
	if _, err := os.Stat("internal/web/views"); os.IsNotExist(err) {
		_ = os.Chdir(filepath.Join("..", ".."))
	}
}

