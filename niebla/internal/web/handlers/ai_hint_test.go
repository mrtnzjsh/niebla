package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"niebla/internal/config"
	"niebla/pkg/ai"
)

func TestAIHintUIHiddenByDefaultUntilEndpointConfigured(t *testing.T) {
	ResetDemoStoresForTest()

	// 1. DefaultConfig has no OpenAI endpoint configured -> AI hint UI elements must NOT appear
	defaultCfg := config.DefaultConfig()
	h := NewCommentsHandler(nil)
	h.SetDependencies(nil, defaultCfg)

	req := httptest.NewRequest(http.MethodGet, "/cl/1/comment-box?file=pkg/auth/token.go&line=10&end_line=14&side=modified&ps=2", nil)
	w := httptest.NewRecorder()
	h.HandleGetCommentBox(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}
	html := w.Body.String()
	if strings.Contains(html, "Provide AI hint") || strings.Contains(html, "Generate Hint") {
		t.Errorf("expected AI hint UI controls to be hidden when no OpenAI endpoint is configured, got:\n%s", html)
	}

	// Calling HandleGenerateAIHint when unconfigured must fail with 400 Bad Request
	genForm := url.Values{
		"cl_id":           {"1"},
		"patchset_number": {"2"},
		"file_path":       {"pkg/auth/token.go"},
		"line_number":     {"10"},
		"end_line_number": {"14"},
		"body":            {"Check token expiration"},
	}
	genReq := httptest.NewRequest(http.MethodPost, "/cl/1/ai-hint/generate", strings.NewReader(genForm.Encode()))
	genReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	genW := httptest.NewRecorder()
	h.HandleGenerateAIHint(genW, genReq)
	if genW.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request when AI endpoint is not configured, got %d", genW.Code)
	}

	// 2. Once an OpenAI-compatible endpoint is configured, AI hint UI elements appear
	configuredCfg := config.DefaultConfig()
	configuredCfg.Critique.AI.Enabled = true
	configuredCfg.Critique.AI.Endpoint = "http://127.0.0.1:11434/v1"
	configuredCfg.Critique.AI.Model = "qwen2.5-coder"
	h.SetDependencies(nil, configuredCfg)

	req2 := httptest.NewRequest(http.MethodGet, "/cl/1/comment-box?file=pkg/auth/token.go&line=10&end_line=14&side=modified&ps=2", nil)
	w2 := httptest.NewRecorder()
	h.HandleGetCommentBox(w2, req2)

	html2 := w2.Body.String()
	if !strings.Contains(html2, "Provide AI hint") || !strings.Contains(html2, "Generate Hint") {
		t.Errorf("expected AI hint UI controls to appear when OpenAI endpoint is configured, got:\n%s", html2)
	}

	// Reset default back to false after test
	h.SetDependencies(nil, config.DefaultConfig())
}

func TestHandleGenerateAIHint_CallsOpenAIEndpoint(t *testing.T) {
	ResetDemoStoresForTest()

	mockLLM := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp, _ := json.Marshal(ai.HintResponse{
			SufficientContext:    true,
			Explanation:          "Validate token expiration before verifying HMAC signature",
			ReplacementStartLine: 12,
			ReplacementEndLine:   14,
			ReplacementCode:      "if time.Now().After(tok.ExpiresAt) {\n\treturn ErrTokenExpired\n}",
		})
		outer, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": string(resp)}},
			},
		})
		_, _ = w.Write(outer)
	}))
	defer mockLLM.Close()

	cfg := config.DefaultConfig()
	cfg.Critique.AI.Enabled = true
	cfg.Critique.AI.Endpoint = mockLLM.URL + "/v1"
	cfg.Critique.AI.APIKey = "test-key"

	h := NewCommentsHandler(nil)
	h.SetDependencies(nil, cfg)

	form := url.Values{
		"cl_id":           {"1"},
		"patchset_number": {"2"},
		"file_path":       {"pkg/auth/token.go"},
		"line_number":     {"12"},
		"end_line_number": {"14"},
		"body":            {"Check expiration before HMAC"},
	}

	req := httptest.NewRequest(http.MethodPost, "/cl/1/ai-hint/generate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	h.HandleGenerateAIHint(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d: %s", w.Code, w.Body.String())
	}

	html := w.Body.String()
	if !strings.Contains(html, "AI Hint Preview") {
		t.Errorf("expected 'AI Hint Preview' in response HTML")
	}
	if !strings.Contains(html, "Validate token expiration before verifying HMAC signature") {
		t.Errorf("expected AI explanation in response HTML")
	}
	if !strings.Contains(html, "Accept Hint into Comment") {
		t.Errorf("expected 'Accept Hint into Comment' button in response HTML")
	}
}

func TestHandleCreateAndApplySuggestion(t *testing.T) {
	ResetDemoStoresForTest()

	h := NewCommentsHandler(nil)

	form := url.Values{
		"cl_id":                 {"1"},
		"patchset_number":       {"2"},
		"file_path":             {"pkg/auth/token.go"},
		"line_number":           {"12"},
		"end_line_number":       {"14"},
		"side":                  {"modified"},
		"author":                {"alice"},
		"body":                  {"Use constant-time comparison"},
		"has_suggestion":        {"true"},
		"suggestion_start_line": {"12"},
		"suggestion_end_line":   {"14"},
		"suggestion_code":       {"if subtle.ConstantTimeCompare(a, b) == 1 {"},
	}

	req := httptest.NewRequest(http.MethodPost, "/cl/1/comments", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleCreateComment(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("HandleCreateComment failed: %d", w.Code)
	}

	// Publish the draft in demo store so the submitter sees "Apply Suggestion"
	demoCommentsMu.Lock()
	var createdID int64
	for k, list := range demoCommentsStore {
		for i := range list {
			list[i].IsDraft = false
			createdID = list[i].ID
			if !strings.Contains(list[i].Body, "```suggestion") {
				t.Errorf("expected dual-stored markdown fence in comment body, got %q", list[i].Body)
			}
		}
		demoCommentsStore[k] = list
	}
	demoCommentsMu.Unlock()

	// Submitter clicks Apply Suggestion
	applyReq := httptest.NewRequest(http.MethodPost, "/cl/1/comments/"+strconv.FormatInt(createdID, 10)+"/apply-suggestion", nil)
	applyW := httptest.NewRecorder()
	h.HandleApplySuggestion(applyW, applyReq)

	if applyW.Code != http.StatusOK {
		t.Fatalf("HandleApplySuggestion failed: %d", applyW.Code)
	}

	applyHTML := applyW.Body.String()
	if !strings.Contains(applyHTML, "Applied in PS 3") {
		t.Errorf("expected 'Applied in PS 3' badge in response HTML, got:\n%s", applyHTML)
	}
	if !strings.Contains(applyHTML, "Resolved") {
		t.Errorf("expected comment thread to auto-resolve on apply")
	}
}

