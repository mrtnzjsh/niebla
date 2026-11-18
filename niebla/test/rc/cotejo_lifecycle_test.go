//go:build rc

package rc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"niebla/pkg/ai"
	"niebla/pkg/tree"
)

func TestCotejoFullReviewLifecycle(t *testing.T) {
	cluster := SetupRCCluster(t)
	defer cluster.Cleanup()

	ctx := context.Background()

	// 1. Seed author's changelist with a source file
	author := "alice"
	reviewer := "bob"

	fileContent := []byte("package main\n\nfunc Run() string {\n\treturn \"initial draft\"\n}\n")
	fileHash := sha256.Sum256(fileContent)

	blobInfo, err := cluster.CASStorage.Put(bytes.NewReader(fileContent))
	if err != nil {
		t.Fatalf("failed to store seed blob in CAS: %v", err)
	}
	if err := cluster.DB.InsertBlob(ctx, blobInfo.HashBytes, blobInfo.Size, blobInfo.StoragePath); err != nil {
		t.Fatalf("failed to insert blob in DB: %v", err)
	}

	entries := []tree.Entry{
		{
			Name:       "main.go",
			Mode:       0100644,
			Size:       uint64(len(fileContent)),
			BlobSHA256: fileHash[:],
		},
	}
	tree.SortEntries(entries)
	rootTreeHash := tree.ComputeTreeHash(entries)
	if err := cluster.DB.InsertTreeNodes(ctx, rootTreeHash, entries); err != nil {
		t.Fatalf("failed to insert tree nodes: %v", err)
	}

	clID, err := cluster.DB.CreateShelvedCL(ctx, author, "feat: implement main runner", rootTreeHash, 0)
	if err != nil {
		t.Fatalf("failed to create shelved CL: %v", err)
	}

	baseURL := cluster.CotejoServer.URL
	client := &http.Client{}

	// Helper to send HTTP requests with auth headers
	sendReq := func(method, endpoint, user string, body io.Reader, contentType string) (*http.Response, string) {
		req, err := http.NewRequest(method, baseURL+endpoint, body)
		if err != nil {
			t.Fatalf("failed to create HTTP request %s %s: %v", method, endpoint, err)
		}
		if user != "" {
			req.Header.Set("X-Remote-User", user)
			req.Header.Set("X-Remote-Email", user+"@example.com")
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		req.Header.Set("HX-Request", "true")

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HTTP request %s %s failed: %v", method, endpoint, err)
		}
		defer resp.Body.Close()
		respBytes, _ := io.ReadAll(resp.Body)
		return resp, string(respBytes)
	}

	// Helper to simulate browser page loads (full layout rendering without HX-Request)
	sendBrowserReq := func(endpoint, user string) (*http.Response, string) {
		req, err := http.NewRequest("GET", baseURL+endpoint, nil)
		if err != nil {
			t.Fatalf("failed to create browser request GET %s: %v", endpoint, err)
		}
		if user != "" {
			req.Header.Set("X-Remote-User", user)
			req.Header.Set("X-Remote-Email", user+"@example.com")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Browser request GET %s failed: %v", endpoint, err)
		}
		defer resp.Body.Close()
		respBytes, _ := io.ReadAll(resp.Body)
		return resp, string(respBytes)
	}

	// 2. Reviewer Management: Add, Remove, and Re-add reviewer 'bob'
	addReviewerForm := url.Values{"reviewer": {reviewer}}.Encode()
	resp, body := sendReq("POST", fmt.Sprintf("/cl/%d/reviewers", clID), author, strings.NewReader(addReviewerForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Errorf("Add reviewer returned unexpected status %d: %s", resp.StatusCode, body)
	}
	reviewers, err := cluster.DB.ListReviewers(ctx, clID)
	if err != nil || len(reviewers) != 1 || reviewers[0].Reviewer != reviewer {
		t.Fatalf("Expected reviewer %s assigned, got %v (err: %v)", reviewer, reviewers, err)
	}

	// Remove reviewer 'bob'
	resp, body = sendReq("DELETE", fmt.Sprintf("/cl/%d/reviewers/%s", clID, reviewer), author, nil, "")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Errorf("Remove reviewer returned unexpected status %d: %s", resp.StatusCode, body)
	}
	reviewers, err = cluster.DB.ListReviewers(ctx, clID)
	if err != nil || len(reviewers) != 0 {
		t.Fatalf("Expected 0 reviewers after removal, got %v (err: %v)", reviewers, err)
	}

	// Re-add reviewer 'bob'
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/reviewers", clID), author, strings.NewReader(addReviewerForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Errorf("Re-add reviewer returned unexpected status %d: %s", resp.StatusCode, body)
	}

	// 3. Reviewer Bob views the CL and verifies UI rendering & static assets
	resp, body = sendReq("GET", fmt.Sprintf("/cl/%d", clID), reviewer, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Reviewer GET /cl/%d failed with status %d", clID, resp.StatusCode)
	}
	if !strings.Contains(body, "implement main runner") {
		t.Errorf("CL detail page did not contain expected subject")
	}
	if !strings.Contains(body, `<link rel="stylesheet" href="/static/styles.css"`) {
		t.Errorf("CL detail page missing stylesheet link /static/styles.css")
	}
	if !strings.Contains(body, `<script src="/static/htmx.min.js"></script>`) {
		t.Errorf("CL detail page missing script tag /static/htmx.min.js")
	}

	// 3b. Verify Index page references static assets
	resp, indexBody := sendBrowserReq("/", reviewer)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / failed with status %d", resp.StatusCode)
	}
	if !strings.Contains(indexBody, `<link rel="stylesheet" href="/static/styles.css"`) {
		t.Errorf("Index page missing stylesheet link /static/styles.css")
	}
	if !strings.Contains(indexBody, `<script src="/static/htmx.min.js"></script>`) {
		t.Errorf("Index page missing script tag /static/htmx.min.js")
	}

	// 3c. Verify embedded static assets resolve with 200 OK, valid MIME types, and non-empty content
	assertStaticAsset := func(path, expectedMIME string) {
		assetResp, err := client.Get(baseURL + path)
		if err != nil {
			t.Fatalf("Failed to fetch static asset %s: %v", path, err)
		}
		defer assetResp.Body.Close()
		if assetResp.StatusCode != http.StatusOK {
			t.Errorf("GET %s returned status %d, expected 200 OK", path, assetResp.StatusCode)
		}
		ct := assetResp.Header.Get("Content-Type")
		if !strings.Contains(ct, expectedMIME) {
			t.Errorf("GET %s Content-Type = %q; expected to contain %q", path, ct, expectedMIME)
		}
		bodyBytes, err := io.ReadAll(assetResp.Body)
		if err != nil {
			t.Fatalf("Failed to read static asset body %s: %v", path, err)
		}
		if len(bodyBytes) == 0 {
			t.Errorf("GET %s returned empty body", path)
		}
		if assetResp.ContentLength <= 0 && int64(len(bodyBytes)) <= 0 {
			t.Errorf("GET %s has invalid Content-Length: %d", path, assetResp.ContentLength)
		}
	}

	assertStaticAsset("/static/styles.css", "text/css")
	assertStaticAsset("/static/htmx.min.js", "application/javascript")

	// 4. Interactive Comment Box retrieval
	resp, body = sendReq("GET", fmt.Sprintf("/cl/%d/comment-box?file=main.go&line=4&side=modified&ps=1", clID), reviewer, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET comment-box failed with status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, fmt.Sprintf("/cl/%d/comments", clID)) {
		t.Errorf("Comment box missing hx-post target /cl/%d/comments, got:\n%s", clID, body)
	}

	// 5. Comment authoring, resolution, and discard lifecycle
	// 5a. Post a temporary draft comment and discard it
	draftForm := url.Values{
		"file_path":   {"main.go"},
		"line_number": {"3"},
		"body":        {"Temporary draft note to be discarded."},
		"patchset":    {"1"},
	}.Encode()
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/comments", clID), reviewer, strings.NewReader(draftForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Post draft comment failed with status %d: %s", resp.StatusCode, body)
	}
	line3Comments, err := cluster.DB.ListCommentsForLine(ctx, clID, "main.go", 3, "modified")
	if err != nil || len(line3Comments) == 0 {
		t.Fatalf("Failed to find created draft comment on line 3: %v", err)
	}
	draftID := line3Comments[0].ID

	// Discard the draft comment
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/comments/%d/discard", clID, draftID), reviewer, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Discard draft failed with status %d: %s", resp.StatusCode, body)
	}
	line3AfterDiscard, err := cluster.DB.ListCommentsForLine(ctx, clID, "main.go", 3, "modified")
	if err != nil || len(line3AfterDiscard) != 0 {
		t.Errorf("Expected line 3 comments to be empty after discard, got %v", line3AfterDiscard)
	}

	// 5b. Post a persistent comment on line 4
	commentForm := url.Values{
		"file_path":   {"main.go"},
		"line_number": {"4"},
		"body":        {"Consider returning a structured error instead of a raw string."},
		"patchset":    {"1"},
	}.Encode()
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/comments", clID), reviewer, strings.NewReader(commentForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Post persistent comment failed with status %d: %s", resp.StatusCode, body)
	}
	line4Comments, err := cluster.DB.ListCommentsForLine(ctx, clID, "main.go", 4, "modified")
	if err != nil || len(line4Comments) == 0 {
		t.Fatalf("Failed to find created comment on line 4: %v", err)
	}
	activeCommentID := line4Comments[0].ID

	// Resolve the comment thread
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/comments/%d/resolve?resolved=true", clID, activeCommentID), reviewer, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Resolve comment failed with status %d: %s", resp.StatusCode, body)
	}
	comm, err := cluster.DB.GetComment(ctx, activeCommentID)
	if err != nil || !comm.Resolved {
		t.Errorf("Expected comment %d to be marked resolved, got %v (err: %v)", activeCommentID, comm, err)
	}

	// 6. AI Hint Generation with Mock OpenAI Endpoint
	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		hintResp, _ := json.Marshal(ai.HintResponse{
			SufficientContext:    true,
			Explanation:          "Return explicit error tuple rather than raw string",
			ReplacementStartLine: 3,
			ReplacementEndLine:   5,
			ReplacementCode:      "func Run() (string, error) {\n\treturn \"initial draft\", nil\n}",
		})
		outer, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": string(hintResp)}},
			},
		})
		_, _ = w.Write(outer)
	}))
	defer mockLLM.Close()

	// Reconfigure Critique AI settings and refresh handler
	cluster.Config.Critique.AI.Enabled = true
	cluster.Config.Critique.AI.Endpoint = mockLLM.URL + "/v1"
	cluster.Config.Critique.AI.APIKey = "test-key"
	cluster.CommentsHandler.SetDependencies(cluster.CASStorage, cluster.Config)

	// Verify comment-box now surfaces AI hint UI
	resp, body = sendReq("GET", fmt.Sprintf("/cl/%d/comment-box?file=main.go&line=4&side=modified&ps=1", clID), reviewer, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET comment-box after AI config failed: %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Provide AI hint") && !strings.Contains(body, "Generate Hint") {
		t.Errorf("Expected AI hint UI controls in comment box after enabling AI, got:\n%s", body)
	}

	// Trigger AI hint generation
	aiHintForm := url.Values{
		"cl_id":           {fmt.Sprintf("%d", clID)},
		"file_path":       {"main.go"},
		"line_number":     {"3"},
		"end_line_number": {"5"},
		"patchset_number": {"1"},
		"body":            {"Return explicit error tuple"},
	}.Encode()
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/ai-hint/generate", clID), reviewer, strings.NewReader(aiHintForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Generate AI hint failed with status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "AI Hint Preview") {
		t.Errorf("Expected 'AI Hint Preview' in response HTML")
	}
	if !strings.Contains(body, "Return explicit error tuple rather than raw string") {
		t.Errorf("Expected AI hint explanation in response HTML")
	}

	// 7. Code Suggestion Application
	// Bob submits a comment with a code suggestion
	suggCode := "func Run() (string, error) {\n\treturn \"initial draft\", nil\n}"
	suggCommentForm := url.Values{
		"file_path":             {"main.go"},
		"line_number":           {"3"},
		"end_line_number":       {"5"},
		"side":                  {"modified"},
		"patchset_number":       {"1"},
		"body":                  {"Suggesting explicit error return tuple."},
		"suggestion_code":       {suggCode},
		"suggestion_start_line": {"3"},
		"suggestion_end_line":   {"5"},
	}.Encode()
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/comments", clID), reviewer, strings.NewReader(suggCommentForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to post suggestion comment: %d: %s", resp.StatusCode, body)
	}

	// Locate the suggestion comment
	line3Comments, err = cluster.DB.ListCommentsForLine(ctx, clID, "main.go", 5, "modified")
	if err != nil || len(line3Comments) == 0 {
		t.Fatalf("Failed to locate suggestion comment on lines 3-5: %v", err)
	}
	suggCommentID := line3Comments[0].ID

	// 8. Reviewer Bob publishes review
	publishForm := url.Values{}.Encode()
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/publish", clID), reviewer, strings.NewReader(publishForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Errorf("Publish review failed with status %d: %s", resp.StatusCode, body)
	}

	// Author Alice applies the published suggestion
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/comments/%d/apply-suggestion", clID, suggCommentID), author, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Apply suggestion failed with status %d: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Applied in PS 2") {
		t.Errorf("Expected 'Applied in PS 2' badge in response HTML, got:\n%s", body)
	}

	// Verify DB state: Patchset 2 created
	latestPS, err := cluster.DB.GetLatestPatchsetNumber(ctx, clID)
	if err != nil || latestPS != 2 {
		t.Fatalf("Expected latest patchset number to be 2, got %d (err: %v)", latestPS, err)
	}

	// Verify file diff reflects the applied suggestion live
	resp, body = sendReq("GET", fmt.Sprintf("/cl/%d/file/main.go", clID), reviewer, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET file diff after applied suggestion failed: %d", resp.StatusCode)
	}
	if !strings.Contains(body, "(string, error)") {
		t.Errorf("Diff does not reflect applied suggestion '(string, error)', got:\n%s", body)
	}

	// 9. Reviewer Bob approves CL
	approvalForm := url.Values{"verdict": {"approved"}}.Encode()
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/approval", clID), reviewer, strings.NewReader(approvalForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Errorf("Approval submission failed with status %d: %s", resp.StatusCode, body)
	}

	// 10. Author Alice views updated status and submits via Cotejo UI
	submitForm := url.Values{}.Encode()
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/submit", clID), author, strings.NewReader(submitForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Errorf("Submit CL via Cotejo returned status %d: %s", resp.StatusCode, body)
	}

	// 11. Verify DB status is 'submitted'
	clRecord, err := cluster.DB.GetCL(ctx, clID)
	if err != nil {
		t.Fatalf("failed to query CL %d: %v", clID, err)
	}
	if clRecord.Status != "submitted" {
		t.Errorf("expected CL %d status to be 'submitted', got %q", clID, clRecord.Status)
	}
	if clRecord.TrunkRevision == nil || *clRecord.TrunkRevision <= 0 {
		t.Errorf("expected CL %d to have valid positive trunk revision, got %v", clID, clRecord.TrunkRevision)
	}

	// 12. Verify Inbox segregation
	resp, body = sendReq("GET", "/cls?tab=merged", author, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /cls?tab=merged failed with status %d", resp.StatusCode)
	}
	if !strings.Contains(body, fmt.Sprintf("CL %d", clID)) && !strings.Contains(body, "implement main runner") {
		t.Errorf("Merged tab did not contain submitted CL %d", clID)
	}
}
