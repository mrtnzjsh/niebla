//go:build rc

package rc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

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

	// 2. Author adds reviewer 'bob'
	addReviewerForm := url.Values{"reviewers": {reviewer}}.Encode()
	resp, body := sendReq("POST", fmt.Sprintf("/cl/%d/reviewers", clID), author, strings.NewReader(addReviewerForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Errorf("Add reviewer returned unexpected status %d: %s", resp.StatusCode, body)
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

	// 4. Reviewer Bob views file diff for main.go
	resp, body = sendReq("GET", fmt.Sprintf("/cl/%d/file/main.go", clID), reviewer, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET file diff failed with status %d", resp.StatusCode)
	}

	// 5. Reviewer Bob posts an inline comment
	commentForm := url.Values{
		"file_path":   {"main.go"},
		"line_number": {"4"},
		"body":        {"Consider returning a structured error instead of a raw string."},
		"patchset":    {"1"},
	}.Encode()
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/comments", clID), reviewer, strings.NewReader(commentForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Post comment failed with status %d: %s", resp.StatusCode, body)
	}

	// 6. Reviewer Bob approves the CL (+1 / LGTM)
	approvalForm := url.Values{"verdict": {"approved"}}.Encode()
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/approval", clID), reviewer, strings.NewReader(approvalForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		t.Errorf("Approval submission failed with status %d: %s", resp.StatusCode, body)
	}

	// 7. Author Alice views updated status and submits via Cotejo UI
	submitForm := url.Values{}.Encode()
	resp, body = sendReq("POST", fmt.Sprintf("/cl/%d/submit", clID), author, strings.NewReader(submitForm), "application/x-www-form-urlencoded")
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusSeeOther {
		t.Errorf("Submit CL via Cotejo returned status %d: %s", resp.StatusCode, body)
	}

	// 8. Verify DB status is 'submitted'
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

	// 9. Verify Inbox segregation
	resp, body = sendReq("GET", "/cls?tab=merged", author, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /cls?tab=merged failed with status %d", resp.StatusCode)
	}
	if !strings.Contains(body, fmt.Sprintf("CL %d", clID)) && !strings.Contains(body, "implement main runner") {
		t.Errorf("Merged tab did not contain submitted CL %d", clID)
	}
}

