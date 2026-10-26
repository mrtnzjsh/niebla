package handlers

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"niebla/internal/config"
	"niebla/internal/db"
	"niebla/internal/groups"
	"niebla/internal/web/diff"
	"niebla/internal/web/views"
	"niebla/pkg/cas"
	"niebla/pkg/events"
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
		CL:                        cl,
		Patchsets:                 patchsets,
		CurrentPatchset:           1,
		BasePatchset:              0,
		Files:                     files,
		SelectedFilePath:          "math.go",
		SelectedFileDiff:          detail,
		CommentsByLine:            commentsMap,
		Approvals:                 []db.Approval{{CLID: 101, Reviewer: "bob", Verdict: "lgtm"}},
		Reviewers:                 []db.ReviewerAssignment{{CLID: 101, Reviewer: "bob", Verdict: "lgtm", UpdatedAt: time.Now()}},
		RequiredApprovals:         1,
		ApprovedCount:             1,
		AllowSubmitWithUnresolved: false,
		UnresolvedComments:        1,
		ActiveReviewer:            "alice",
		ReviewedFilesCount:        1,
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
	if !strings.Contains(clDetailHTML, "Approvals: 1 of 1 required") {
		t.Errorf("expected required approvals indicator in CLDetail, got %s", clDetailHTML)
	}
	if !strings.Contains(clDetailHTML, "bob") {
		t.Errorf("expected reviewer bob in reviewers bar, got %s", clDetailHTML)
	}
	if !strings.Contains(clDetailHTML, "Submit CL") {
		t.Errorf("expected Submit CL button in CLDetail, got %s", clDetailHTML)
	}
	if !strings.Contains(clDetailHTML, "unresolved-comments-modal") {
		t.Errorf("expected unresolved comments modal dialog in CLDetail, got %s", clDetailHTML)
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
	if !strings.Contains(aliceViewAfter, "3 Unresolved Comments") {
		t.Errorf("expected '3 Unresolved Comments' in overview bar, got: %s", aliceViewAfter)
	}
	if !strings.Contains(aliceViewAfter, "LGTM / Approve") {
		t.Errorf("expected approval buttons on CL view")
	}

	// 6. Alice opens the accordion diff for engine.go via diff-content endpoint -> published comment is in the diff!
	reqDiff := httptest.NewRequest("GET", "/cl/1/diff-content?file=internal/web/diff/engine.go&reviewer=alice", nil)
	recDiff := httptest.NewRecorder()
	clHandler.HandleCLDiffContent(recDiff, reqDiff)
	if recDiff.Code != http.StatusOK {
		t.Fatalf("HandleCLDiffContent returned status %d", recDiff.Code)
	}
	if !strings.Contains(recDiff.Body.String(), "First draft note") {
		t.Errorf("expected published comment 'First draft note' in fresh diff-content")
	}
}

func TestFileReviewEndpoints(t *testing.T) {
	ResetDemoStoresForTest()
	handler := NewCLHandler(nil, nil)

	// 1. Visit CL page as alice without file selection -> 0 of 4 reviewed by default
	reqAlice := httptest.NewRequest("GET", "/cl/1?reviewer=alice", nil)
	recAlice := httptest.NewRecorder()
	handler.HandleCLView(recAlice, reqAlice)

	if recAlice.Code != http.StatusOK {
		t.Fatalf("HandleCLView as alice returned status %d", recAlice.Code)
	}
	bodyAlice := recAlice.Body.String()
	if !strings.Contains(bodyAlice, "0 of 4 reviewed by alice") {
		t.Errorf("expected '0 of 4 reviewed by alice', got: %s", bodyAlice)
	}

	// 2. Alice visits a file diff directly in standalone tab -> file is marked reviewed
	reqAliceFile := httptest.NewRequest("GET", "/cl/1/file/internal/web/diff/engine.go?reviewer=alice", nil)
	recAliceFile := httptest.NewRecorder()
	handler.HandleCLFileDiff(recAliceFile, reqAliceFile)
	if recAliceFile.Code != http.StatusOK {
		t.Fatalf("HandleCLFileDiff as alice returned status %d", recAliceFile.Code)
	}
	bodyAliceFile := recAliceFile.Body.String()
	if !strings.Contains(bodyAliceFile, "reviewed") {
		t.Errorf("expected file review badge to show reviewed in dedicated tab")
	}

	// Verify CL overview shows 1 of 4 reviewed by alice
	reqAliceOverview := httptest.NewRequest("GET", "/cl/1?reviewer=alice", nil)
	recAliceOverview := httptest.NewRecorder()
	handler.HandleCLView(recAliceOverview, reqAliceOverview)
	if !strings.Contains(recAliceOverview.Body.String(), "1 of 4 reviewed by alice") {
		t.Errorf("expected '1 of 4 reviewed by alice' on CL overview, got: %s", recAliceOverview.Body.String())
	}

	// 3. Bob visits a different file in standalone tab -> 1 of 4 reviewed by bob
	reqBob := httptest.NewRequest("GET", "/cl/1/file/cmd/critique/main.go?reviewer=bob", nil)
	recBob := httptest.NewRecorder()
	handler.HandleCLFileDiff(recBob, reqBob)

	if recBob.Code != http.StatusOK {
		t.Fatalf("HandleCLFileDiff as bob returned status %d", recBob.Code)
	}
	bodyBob := recBob.Body.String()
	if !strings.Contains(bodyBob, "reviewed") {
		t.Errorf("expected file review badge to show reviewed in dedicated tab for bob")
	}

	// Verify CL overview shows 1 of 4 reviewed by bob
	reqBobOverview := httptest.NewRequest("GET", "/cl/1?reviewer=bob", nil)
	recBobOverview := httptest.NewRecorder()
	handler.HandleCLView(recBobOverview, reqBobOverview)
	if !strings.Contains(recBobOverview.Body.String(), "1 of 4 reviewed by bob") {
		t.Errorf("expected '1 of 4 reviewed by bob' on CL overview, got: %s", recBobOverview.Body.String())
	}

	// 4. Mark another file as reviewed for bob via POST /cl/1/file-review
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

	// 5. Toggle file back to not reviewed for bob
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

func TestReviewerManagementEndpoints(t *testing.T) {
	ResetDemoStoresForTest()
	handler := NewCLHandler(nil, nil)

	cfg := &config.Config{
		Critique: config.CritiqueConfig{
			Groups: config.CritiqueGroupConfig{
				Mode: "static",
				Static: map[string][]string{
					"backend": {"charlie", "david"},
				},
			},
		},
	}
	resolver := groups.NewResolver(cfg.Critique.Groups)
	handler.SetDependencies(cfg, resolver)

	// 1. Add single reviewer "alice"
	form := url.Values{"reviewer": {"alice"}}
	reqAddAlice := httptest.NewRequest("POST", "/cl/1/reviewers", strings.NewReader(form.Encode()))
	reqAddAlice.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqAddAlice.SetPathValue("id", "1")
	recAddAlice := httptest.NewRecorder()
	handler.HandleAddReviewers(recAddAlice, reqAddAlice)

	if recAddAlice.Code != http.StatusOK {
		t.Fatalf("HandleAddReviewers returned status %d: %s", recAddAlice.Code, recAddAlice.Body.String())
	}
	bodyAddAlice := recAddAlice.Body.String()
	if !strings.Contains(bodyAddAlice, "alice") {
		t.Errorf("expected 'alice' in reviewers bar, got: %s", bodyAddAlice)
	}

	// 2. Add group "@backend" along with individual user "eve"
	formGroup := url.Values{"reviewer": {"@backend,eve"}}
	reqAddGroup := httptest.NewRequest("POST", "/cl/1/reviewers", strings.NewReader(formGroup.Encode()))
	reqAddGroup.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqAddGroup.SetPathValue("id", "1")
	recAddGroup := httptest.NewRecorder()
	handler.HandleAddReviewers(recAddGroup, reqAddGroup)

	if recAddGroup.Code != http.StatusOK {
		t.Fatalf("HandleAddReviewers group returned status %d: %s", recAddGroup.Code, recAddGroup.Body.String())
	}
	bodyAddGroup := recAddGroup.Body.String()
	for _, user := range []string{"alice", "charlie", "david", "eve"} {
		if !strings.Contains(bodyAddGroup, user) {
			t.Errorf("expected reviewer %q in reviewers bar after group expansion, got: %s", user, bodyAddGroup)
		}
	}

	// 3. Remove reviewer "charlie"
	reqRemoveCharlie := httptest.NewRequest("POST", "/cl/1/reviewers/charlie/remove", nil)
	reqRemoveCharlie.SetPathValue("id", "1")
	reqRemoveCharlie.SetPathValue("user", "charlie")
	recRemoveCharlie := httptest.NewRecorder()
	handler.HandleRemoveReviewer(recRemoveCharlie, reqRemoveCharlie)

	if recRemoveCharlie.Code != http.StatusOK {
		t.Fatalf("HandleRemoveReviewer returned status %d: %s", recRemoveCharlie.Code, recRemoveCharlie.Body.String())
	}
	bodyRemoveCharlie := recRemoveCharlie.Body.String()
	if strings.Contains(bodyRemoveCharlie, "charlie") {
		t.Errorf("expected 'charlie' to be removed from reviewers bar, got: %s", bodyRemoveCharlie)
	}
	if !strings.Contains(bodyRemoveCharlie, "david") {
		t.Errorf("expected 'david' to remain in reviewers bar, got: %s", bodyRemoveCharlie)
	}
}

func TestSubmitCLEndpointGuards(t *testing.T) {
	ResetDemoStoresForTest()
	handler := NewCLHandler(nil, nil)

	reqApp := 2
	allowUnres := false
	cfg := &config.Config{
		Critique: config.CritiqueConfig{
			RequiredApprovals:                 &reqApp,
			AllowSubmitWithUnresolvedComments: &allowUnres,
		},
	}
	handler.SetDependencies(cfg, nil)

	// Step 1: Seed an unresolved comment in demo store
	demoCommentsMu.Lock()
	demoCommentsStore["1:main.go:1:modified"] = []db.Comment{
		{
			ID:       10,
			CLID:     1,
			Author:   "reviewer1",
			Body:     "Please fix this nil check",
			Resolved: false,
			IsDraft:  false,
		},
	}
	demoCommentsMu.Unlock()

	// Attempt submit with unresolved comment
	reqSub1 := httptest.NewRequest("POST", "/cl/1/submit", nil)
	reqSub1.SetPathValue("id", "1")
	recSub1 := httptest.NewRecorder()
	handler.HandleSubmitCL(recSub1, reqSub1)

	if recSub1.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when submitting with unresolved comments, got %d", recSub1.Code)
	}
	if !strings.Contains(recSub1.Body.String(), "unresolved comment thread") {
		t.Errorf("expected unresolved comment error message, got: %s", recSub1.Body.String())
	}

	// Step 2: Mark comment as resolved, but 0 approvals (requires 2)
	demoCommentsMu.Lock()
	demoCommentsStore["1:main.go:1:modified"][0].Resolved = true
	demoCommentsMu.Unlock()

	reqSub2 := httptest.NewRequest("POST", "/cl/1/submit", nil)
	reqSub2.SetPathValue("id", "1")
	recSub2 := httptest.NewRecorder()
	handler.HandleSubmitCL(recSub2, reqSub2)

	if recSub2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when approvals < required, got %d", recSub2.Code)
	}
	if !strings.Contains(recSub2.Body.String(), "requires 2 approval(s) before submission (found 0)") {
		t.Errorf("expected approval requirement error message, got: %s", recSub2.Body.String())
	}

	// Step 3: Record 1 approval ("alice", "lgtm"). Still 1 of 2 required.
	demoApprovalsMu.Lock()
	demoApprovalsStore[1] = []db.Approval{
		{CLID: 1, Reviewer: "alice", Verdict: "lgtm", UpdatedAt: time.Now()},
	}
	demoApprovalsMu.Unlock()

	reqSub3 := httptest.NewRequest("POST", "/cl/1/submit", nil)
	reqSub3.SetPathValue("id", "1")
	recSub3 := httptest.NewRecorder()
	handler.HandleSubmitCL(recSub3, reqSub3)

	if recSub3.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when 1 of 2 approvals met, got %d", recSub3.Code)
	}
	if !strings.Contains(recSub3.Body.String(), "requires 2 approval(s) before submission (found 1)") {
		t.Errorf("expected approval requirement error message, got: %s", recSub3.Body.String())
	}

	// Step 4: Record 2nd approval ("bob", "approved"). Now 2 of 2 met.
	demoApprovalsMu.Lock()
	demoApprovalsStore[1] = append(demoApprovalsStore[1], db.Approval{
		CLID: 1, Reviewer: "bob", Verdict: "approved", UpdatedAt: time.Now(),
	})
	demoApprovalsMu.Unlock()

	reqSub4 := httptest.NewRequest("POST", "/cl/1/submit", nil)
	reqSub4.SetPathValue("id", "1")
	recSub4 := httptest.NewRecorder()
	handler.HandleSubmitCL(recSub4, reqSub4)

	if recSub4.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 See Other on successful submit, got %d: %s", recSub4.Code, recSub4.Body.String())
	}
	if loc := recSub4.Header().Get("Location"); loc != "/cl/1" {
		t.Errorf("expected redirect to /cl/1, got: %s", loc)
	}

	// Step 5: Verify subsequent submit is rejected because CL is now 'submitted'
	reqSub5 := httptest.NewRequest("POST", "/cl/1/submit", nil)
	reqSub5.SetPathValue("id", "1")
	recSub5 := httptest.NewRecorder()
	handler.HandleSubmitCL(recSub5, reqSub5)

	if recSub5.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when submitting already submitted CL, got %d", recSub5.Code)
	}
	if !strings.Contains(recSub5.Body.String(), "cannot be submitted because its status is \"submitted\"") {
		t.Errorf("expected already submitted error message, got: %s", recSub5.Body.String())
	}
}

func TestSubmitCLApprovalOverrideAndCheckmarks(t *testing.T) {
	ResetDemoStoresForTest()
	handler := NewCLHandler(nil, nil)

	reqApp := 2
	allowUnres := true
	cfg := &config.Config{
		Critique: config.CritiqueConfig{
			RequiredApprovals:                 &reqApp,
			AllowSubmitWithUnresolvedComments: &allowUnres,
		},
	}
	handler.SetDependencies(cfg, nil)

	// 1. Submit with 0 approvals and no justification -> fails
	reqNoJust := httptest.NewRequest("POST", "/cl/1/submit", nil)
	reqNoJust.SetPathValue("id", "1")
	recNoJust := httptest.NewRecorder()
	handler.HandleSubmitCL(recNoJust, reqNoJust)
	if recNoJust.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", recNoJust.Code)
	}

	// 2. Submit with justification > 400 chars -> fails
	longJust := strings.Repeat("a", 401)
	formDataLong := url.Values{"justification": {longJust}}
	reqLong := httptest.NewRequest("POST", "/cl/1/submit", strings.NewReader(formDataLong.Encode()))
	reqLong.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqLong.SetPathValue("id", "1")
	recLong := httptest.NewRecorder()
	handler.HandleSubmitCL(recLong, reqLong)
	if recLong.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for >400 chars justification, got %d", recLong.Code)
	}
	if !strings.Contains(recLong.Body.String(), "exceeds 400 characters") {
		t.Errorf("expected 400 char error message, got %s", recLong.Body.String())
	}

	// 3. Submit with valid justification -> succeeds with 303
	validJust := "Emergency critical fix approved by team lead out-of-band"
	formDataValid := url.Values{"justification": {validJust}}
	reqValid := httptest.NewRequest("POST", "/cl/1/submit", strings.NewReader(formDataValid.Encode()))
	reqValid.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reqValid.SetPathValue("id", "1")
	recValid := httptest.NewRecorder()
	handler.HandleSubmitCL(recValid, reqValid)
	if recValid.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 See Other on override submit, got %d: %s", recValid.Code, recValid.Body.String())
	}

	// 4. Test View markup rendering (Checkmarks & Modal)
	ResetDemoStoresForTest()
	reqView := httptest.NewRequest("GET", "/cl/1", nil)
	recView := httptest.NewRecorder()
	handler.HandleCLView(recView, reqView)

	if recView.Code != http.StatusOK {
		t.Fatalf("HandleCLView returned %d", recView.Code)
	}
	html := recView.Body.String()

	// Verify modal title
	if !strings.Contains(html, "Override Approvals and Submit") {
		t.Errorf("expected 'Override Approvals and Submit' modal title in HTML")
	}

	// Verify textarea placeholder
	if !strings.Contains(html, `placeholder="Enter approval override justification..."`) {
		t.Errorf("expected placeholder 'Enter approval override justification...' in HTML")
	}

	// Verify character counter
	if !strings.Contains(html, `0 / 400`) {
		t.Errorf("expected '0 / 400' character counter in HTML")
	}

	// Verify modal submit button
	if !strings.Contains(html, `id="override-submit-btn"`) || !strings.Contains(html, `disabled`) {
		t.Errorf("expected disabled override-submit-btn in HTML")
	}

	// Verify Submit button checkmarks
	if !strings.Contains(html, `stroke="#000000"`) || !strings.Contains(html, `text-black`) {
		t.Errorf("expected black checkmarks for missing approvals in HTML")
	}

	// Verify button click triggers override-approvals-modal
	if !strings.Contains(html, `document.getElementById('override-approvals-modal')`) || !strings.Contains(html, `m.showModal()`) {
		t.Errorf("expected onclick to open override-approvals-modal in HTML")
	}
}

func TestCLViewNoDiffByDefaultAndAccordionMarkup(t *testing.T) {
	ResetDemoStoresForTest()
	handler := NewCLHandler(nil, nil)

	req := httptest.NewRequest("GET", "/cl/1", nil)
	rec := httptest.NewRecorder()
	handler.HandleCLView(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleCLView returned status %d", rec.Code)
	}

	body := rec.Body.String()

	// 1. No diff is rendered on the page by default
	if strings.Contains(body, "Expand All Lines") {
		t.Errorf("expected no diff view to be open by default on /cl/1")
	}

	// 2. All accordion rows should have class="hidden"
	if !strings.Contains(body, `id="accordion-row-internal-web-diff-engine-go" class="border-b border-slate-800 bg-[#090d16] hidden"`) {
		t.Errorf("expected accordion-row-internal-web-diff-engine-go to be hidden by default")
	}

	// 3. Chevrons and toggleFileAccordion should be present
	if !strings.Contains(body, `id="chevron-internal-web-diff-engine-go"`) {
		t.Errorf("expected chevron icon for internal/web/diff/engine.go")
	}
	if !strings.Contains(body, `toggleFileAccordion('1', 'internal-web-diff-engine-go', 'internal/web/diff/engine.go'`) {
		t.Errorf("expected toggleFileAccordion call on file row")
	}

	// 4. Filename links should open in new tab with target="_blank" and handleFilenameClick
	if !strings.Contains(body, `target="_blank"`) {
		t.Errorf("expected target=\"_blank\" on file links")
	}
	if !strings.Contains(body, `handleFilenameClick(event, 'internal-web-diff-engine-go')`) {
		t.Errorf("expected handleFilenameClick on file link")
	}

	// 5. Review badge cell should have stopPropagation
	if !strings.Contains(body, `onclick="event.stopPropagation()"`) {
		t.Errorf("expected stopPropagation on file review cell")
	}
}

func TestCLDiffContentEndpoint(t *testing.T) {
	ResetDemoStoresForTest()
	handler := NewCLHandler(nil, nil)

	// 1. Missing file parameter returns 400
	reqMissing := httptest.NewRequest("GET", "/cl/1/diff-content", nil)
	recMissing := httptest.NewRecorder()
	handler.HandleCLDiffContent(recMissing, reqMissing)
	if recMissing.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing file parameter, got: %d", recMissing.Code)
	}

	// 2. Valid request returns 200 with fresh DiffView component
	req := httptest.NewRequest("GET", "/cl/1/diff-content?file=internal/web/diff/engine.go&ps=2&base_ps=0", nil)
	rec := httptest.NewRecorder()
	handler.HandleCLDiffContent(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleCLDiffContent returned status %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "internal/web/diff/engine.go") {
		t.Errorf("expected file path in diff content")
	}
	if !strings.Contains(body, "Expand All Lines") {
		t.Errorf("expected Expand All Lines button in diff content")
	}
	// Check that comment rows are scoped by file path
	if !strings.Contains(body, "comment-row-internal-web-diff-engine-go-") {
		t.Errorf("expected file-scoped comment row IDs in diff content")
	}
}

func TestCLDiffContentFreshCommentSync(t *testing.T) {
	ResetDemoStoresForTest()
	clHandler := NewCLHandler(nil, nil)
	commentsHandler := NewCommentsHandler(nil)

	// 1. Add draft comment on cmd/critique/main.go
	form := strings.NewReader("cl_id=1&patchset_number=2&file_path=cmd/critique/main.go&line_number=10&side=modified&author=alice&body=Fresh+comment+sync+check")
	reqComment := httptest.NewRequest("POST", "/cl/1/comments", form)
	reqComment.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recComment := httptest.NewRecorder()
	commentsHandler.HandleCreateComment(recComment, reqComment)
	if recComment.Code != http.StatusOK {
		t.Fatalf("HandleCreateComment failed: %d", recComment.Code)
	}

	// 2. Fetch fresh diff-content for cmd/critique/main.go as alice
	reqDiff := httptest.NewRequest("GET", "/cl/1/diff-content?file=cmd/critique/main.go&ps=2&base_ps=0&reviewer=alice", nil)
	recDiff := httptest.NewRecorder()
	clHandler.HandleCLDiffContent(recDiff, reqDiff)

	if recDiff.Code != http.StatusOK {
		t.Fatalf("HandleCLDiffContent returned status %d", recDiff.Code)
	}

	body := recDiff.Body.String()
	if !strings.Contains(body, "Fresh comment sync check") {
		t.Errorf("expected newly saved draft comment to be present in fresh diff-content pull")
	}
}

func TestCLFileDiffStandaloneTab(t *testing.T) {
	ResetDemoStoresForTest()
	handler := NewCLHandler(nil, nil)

	// Open /cl/1/file/internal/web/diff/engine.go
	req := httptest.NewRequest("GET", "/cl/1/file/internal/web/diff/engine.go?ps=2&base_ps=0", nil)
	rec := httptest.NewRecorder()
	handler.HandleCLFileDiff(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("HandleCLFileDiff returned status %d", rec.Code)
	}

	body := rec.Body.String()

	// 1. Dedicated file header with navigation back to CL
	if !strings.Contains(body, "Back to CL 1") {
		t.Errorf("expected back link 'Back to CL 1' in dedicated file tab")
	}
	if !strings.Contains(body, "internal/web/diff/engine.go") {
		t.Errorf("expected targeted file path in dedicated file tab")
	}

	// 2. The diff for this file is rendered with review badge and reply controls
	if !strings.Contains(body, "Expand All Lines") {
		t.Errorf("expected diff to be rendered in standalone file tab")
	}
	if !strings.Contains(body, "file-review-badge-internal-web-diff-engine-go") {
		t.Errorf("expected file review badge in standalone file tab")
	}
	if !strings.Contains(body, "cl-reply-btn") {
		t.Errorf("expected reply button in standalone file tab")
	}

	// 3. Dedicated view does NOT include CL description fields or other file links
	if strings.Contains(body, "Critique: fast server-authoritative code review web UI and diff visualizer") {
		t.Errorf("dedicated file tab should NOT contain CL description fields")
	}
	if strings.Contains(body, "cmd/critique/main.go") {
		t.Errorf("dedicated file tab should NOT contain links to other files")
	}
	if strings.Contains(body, "assets/css/input.css") {
		t.Errorf("dedicated file tab should NOT contain links to other files")
	}
}

func TestDiffHeaderAndLinesChangedConsistency(t *testing.T) {
	ResetDemoStoresForTest()
	handler := NewCLHandler(nil, nil)

	// 1. Fetch CL 1 main page
	reqCL := httptest.NewRequest("GET", "/cl/1", nil)
	reqCL.SetPathValue("id", "1")
	recCL := httptest.NewRecorder()
	handler.HandleCLView(recCL, reqCL)

	if recCL.Code != http.StatusOK {
		t.Fatalf("HandleCLView failed: %d", recCL.Code)
	}
	clBody := recCL.Body.String()

	// 2. Fetch inline diff-content for engine.go
	reqDiff := httptest.NewRequest("GET", "/cl/1/diff-content?file=internal/web/diff/engine.go&ps=2&base_ps=0", nil)
	recDiff := httptest.NewRecorder()
	handler.HandleCLDiffContent(recDiff, reqDiff)

	if recDiff.Code != http.StatusOK {
		t.Fatalf("HandleCLDiffContent failed: %d", recDiff.Code)
	}
	diffBody := recDiff.Body.String()

	// 3. Status badge should NOT be repeated in diff header
	// Diff header should contain the file path directly without a status badge next to it
	if strings.Contains(diffBody, `<!-- File Diff Header -->`) && strings.Contains(diffBody, `<span class="px-2 py-0.5 rounded text-xs font-bold`) {
		t.Errorf("Diff header should not contain a duplicate status badge")
	}

	// 4. Sticky top-12 and border-t should not exist on diff header, preventing empty vertical gap
	if strings.Contains(diffBody, "sticky top-12") {
		t.Errorf("Diff header should not have sticky top-12 which creates empty gap")
	}
	if strings.Contains(diffBody, "border-t border-slate-800") {
		t.Errorf("DiffView outer container should not have border-t")
	}

	// 5. Lines changed numbers should match between the file bar and diff header
	// Compute expected detail from shared helper
	expectedDetail, err := computeDemoFileDiff("internal/web/diff/engine.go", "M")
	if err != nil || expectedDetail == nil {
		t.Fatalf("failed to compute expected demo diff: %v", err)
	}
	expectedStat := fmt.Sprintf("+%d", expectedDetail.Additions)

	if !strings.Contains(clBody, expectedStat) {
		t.Errorf("CL files table bar should contain dynamically computed additions %s", expectedStat)
	}
	if !strings.Contains(diffBody, expectedStat) {
		t.Errorf("Diff header should contain dynamically computed additions %s", expectedStat)
	}
}

func TestCLHandlerEventEmission(t *testing.T) {
	ResetDemoStoresForTest()
	rec := events.NewRecordingEmitter()

	handler := NewCLHandler(nil, nil)
	handler.SetEmitter(rec)

	// 1. Add Reviewers -> emits niebla.cl.reviewers_assigned
	addRevReq := httptest.NewRequest("POST", "/cl/1/reviewers?reviewer=charlie", nil)
	addRevReq.SetPathValue("id", "1")
	addRevRec := httptest.NewRecorder()
	handler.HandleAddReviewers(addRevRec, addRevReq)

	evts := rec.Events()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event after adding reviewer, got %d", len(evts))
	}
	if evts[0].Type != events.TypeReviewersAssigned {
		t.Errorf("expected event type %s, got %s", events.TypeReviewersAssigned, evts[0].Type)
	}
	if evts[0].Subject != "1" {
		t.Errorf("expected subject '1', got %s", evts[0].Subject)
	}

	// 2. Submit Approval -> emits niebla.cl.approval_updated
	rec.Clear()
	appReq := httptest.NewRequest("POST", "/cl/1/approval?verdict=lgtm&reviewer=charlie", nil)
	appReq.SetPathValue("id", "1")
	appRec := httptest.NewRecorder()
	handler.HandleApproval(appRec, appReq)

	evts = rec.Events()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event after approval, got %d", len(evts))
	}
	if evts[0].Type != events.TypeApprovalUpdated {
		t.Errorf("expected event type %s, got %s", events.TypeApprovalUpdated, evts[0].Type)
	}

	// 3. Publish Review -> emits niebla.cl.comments_published (and niebla.cl.approval_updated if verdict)
	rec.Clear()
	form := url.Values{}
	form.Set("author", "charlie")
	form.Set("message", "Looks great to me!")
	pubReq := httptest.NewRequest("POST", "/cl/1/reply", strings.NewReader(form.Encode()))
	pubReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	pubReq.SetPathValue("id", "1")
	pubRec := httptest.NewRecorder()
	handler.HandlePublishReview(pubRec, pubReq)

	evts = rec.Events()
	if len(evts) == 0 {
		t.Fatalf("expected comments published event, got none")
	}
	if evts[0].Type != events.TypeCommentsPublished {
		t.Errorf("expected event type %s, got %s", events.TypeCommentsPublished, evts[0].Type)
	}

	// 4. Submit CL -> emits niebla.cl.submitted
	rec.Clear()
	trueVal := true
	cfg := config.DefaultConfig()
	cfg.Critique.AllowSubmitWithUnresolvedComments = &trueVal
	handler.SetDependencies(cfg, nil)

	subReq := httptest.NewRequest("POST", "/cl/1/submit", nil)
	subReq.SetPathValue("id", "1")
	subRec := httptest.NewRecorder()
	handler.HandleSubmitCL(subRec, subReq)

	evts = rec.Events()
	if len(evts) != 1 {
		t.Fatalf("expected 1 event after submit, got %d", len(evts))
	}
	if evts[0].Type != events.TypeCLSubmitted {
		t.Errorf("expected event type %s, got %s", events.TypeCLSubmitted, evts[0].Type)
	}
}

func TestCLRebaseConflictAndStackBreadcrumbs(t *testing.T) {
	parentCL := int64(10002)
	conflictDetails := "pkg/auth/token.go (2 conflict hunks)"
	data := views.CLDetailData{
		CL: &db.Changelist{
			CLID:                  10003,
			Author:                "alice",
			Description:           "Implement token verification",
			RootTreeHash:          []byte{0x1, 0x2, 0x3},
			ParentCL:              &parentCL,
			Status:                "shelved",
			CommittedAt:           time.Now(),
			NeedsRebase:           true,
			RebaseConflictDetails: &conflictDetails,
		},
		CurrentPatchset:   1,
		Patchsets:         []db.Patchset{{PatchsetNumber: 1}},
		RequiredApprovals: 1,
		ApprovedCount:     1,
	}

	var buf bytes.Buffer
	err := views.CLDetail(data).Render(context.Background(), &buf)
	if err != nil {
		t.Fatalf("failed to render CLDetail: %v", err)
	}
	html := buf.String()

	// Verify Stack breadcrumbs
	if !strings.Contains(html, "Stack:") {
		t.Errorf("expected Stack: breadcrumbs in HTML")
	}
	if !strings.Contains(html, "#10002") {
		t.Errorf("expected Parent CL #10002 link in HTML")
	}
	if !strings.Contains(html, "CL #10003 (Active)") {
		t.Errorf("expected active CL #10003 badge in HTML")
	}

	// Verify Rebase Conflict alert banner
	if !strings.Contains(html, "Rebase Required: Parent CL #10002 updated with conflicts") {
		t.Errorf("expected rebase required banner in HTML")
	}
	if !strings.Contains(html, "niebla sync") {
		t.Errorf("expected niebla sync instruction in HTML")
	}
	if !strings.Contains(html, conflictDetails) {
		t.Errorf("expected conflict details %q in HTML", conflictDetails)
	}

	// Verify Rebase Conflict button in place of normal Submit
	if !strings.Contains(html, "Rebase Conflict") {
		t.Errorf("expected 'Rebase Conflict' disabled button text in HTML")
	}
}

func init() {
	// Ensure working directory is repo root if running from submodule
	if _, err := os.Stat("internal/web/views"); os.IsNotExist(err) {
		_ = os.Chdir(filepath.Join("..", ".."))
	}
}

