package handlers

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"niebla/internal/config"
	"niebla/internal/web/auth"
)

func TestCIHandler_HTTPStatusReporting(t *testing.T) {
	ciHandler := NewCIHandler(nil) // demo in-memory mode

	// 1. Invalid method
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ci/status", nil)
	w := httptest.NewRecorder()
	ciHandler.HandlePostCheckRunStatus(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405 Method Not Allowed, got %d", w.Code)
	}

	// 2. Missing cl_id
	badBody := []byte(`{"runner_name": "test-runner", "status": "passed"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ci/status", bytes.NewReader(badBody))
	w = httptest.NewRecorder()
	ciHandler.HandlePostCheckRunStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for missing cl_id, got %d", w.Code)
	}

	// 3. Invalid status
	badStatusBody := []byte(`{"cl_id": 100, "runner_name": "test-runner", "status": "unknown_status"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ci/status", bytes.NewReader(badStatusBody))
	w = httptest.NewRecorder()
	ciHandler.HandlePostCheckRunStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for invalid status, got %d", w.Code)
	}

	// 4. Invalid log_blob_sha256 hex
	badHexBody := []byte(`{"cl_id": 100, "runner_name": "test-runner", "status": "passed", "log_blob_sha256": "not-valid-hex"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ci/status", bytes.NewReader(badHexBody))
	w = httptest.NewRecorder()
	ciHandler.HandlePostCheckRunStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for invalid hex sha256, got %d", w.Code)
	}

	// 5. Valid report: queued
	validBody := []byte(`{
		"cl_id": 100,
		"patchset_number": 1,
		"runner_name": "local-runner",
		"provider": "local",
		"status": "queued"
	}`)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ci/status", bytes.NewReader(validBody))
	w = httptest.NewRecorder()
	ciHandler.HandlePostCheckRunStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	// Check GET status: pending=1, green=false
	req = httptest.NewRequest(http.MethodGet, "/api/v1/ci/status?cl_id=100&patchset=1", nil)
	w = httptest.NewRecorder()
	ciHandler.HandleGetCheckRuns(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on GET, got %d: %s", w.Code, w.Body.String())
	}

	var getResp struct {
		Green   bool `json:"green"`
		Total   int  `json:"total"`
		Pending int  `json:"pending"`
		Failed  int  `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("failed to decode GET response: %v", err)
	}
	if getResp.Green || getResp.Total != 1 || getResp.Pending != 1 || getResp.Failed != 0 {
		t.Fatalf("unexpected GET response: %+v", getResp)
	}

	// 6. Update to passed with valid 64-char sha256 hex
	dummyHash := make([]byte, 32)
	dummyHash[0] = 0xca
	dummyHash[31] = 0xfe
	hexHash := hex.EncodeToString(dummyHash)

	updateBody, _ := json.Marshal(map[string]any{
		"cl_id":           100,
		"patchset_number": 1,
		"runner_name":     "local-runner",
		"provider":        "local",
		"status":          "passed",
		"external_url":    "https://ci.niebla.dev/build/42",
		"log_blob_sha256": hexHash,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/ci/status", bytes.NewReader(updateBody))
	w = httptest.NewRecorder()
	ciHandler.HandlePostCheckRunStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on update, got %d: %s", w.Code, w.Body.String())
	}

	// Check GET status again: total=1, pending=0, failed=0 -> green=true
	req = httptest.NewRequest(http.MethodGet, "/api/v1/ci/status?cl_id=100&patchset=1", nil)
	w = httptest.NewRecorder()
	ciHandler.HandleGetCheckRuns(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on GET, got %d", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("failed to decode GET response: %v", err)
	}
	if !getResp.Green || getResp.Total != 1 || getResp.Pending != 0 || getResp.Failed != 0 {
		t.Fatalf("expected green=true, total=1, pending=0, failed=0; got: %+v", getResp)
	}
}

func TestSubmitCL_CheckRunsGateInCotejo(t *testing.T) {
	ResetDemoStoresForTest()

	trueVal := true
	cfg := config.DefaultConfig()
	cfg.Critique.AllowSubmitWithUnresolvedComments = &trueVal
	cfg.Critique.RequireGreenCheckRuns = &trueVal

	authResolver := auth.NewUserIdentityResolver(config.CritiqueAuthConfig{Mode: "local"})
	clHandler := NewCLHandler(nil, nil, authResolver)
	clHandler.SetDependencies(cfg, nil)
	ciHandler := NewCIHandler(nil)

	// 1. Submit with 0 check runs -> blocked
	req := httptest.NewRequest("POST", "/cl/1/submit", nil)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	clHandler.HandleSubmitCL(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when 0 check runs exist, got %d", rec.Code)
	}

	// 2. Submit with running check run -> blocked
	postRun := func(status string) {
		body, _ := json.Marshal(map[string]any{
			"cl_id":           1,
			"patchset_number": 1,
			"runner_name":     "local-runner",
			"provider":        "local",
			"status":          status,
		})
		r := httptest.NewRequest("POST", "/api/v1/ci/status", bytes.NewReader(body))
		w := httptest.NewRecorder()
		ciHandler.HandlePostCheckRunStatus(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("failed to post check run status: %d", w.Code)
		}
	}

	postRun("running")
	rec = httptest.NewRecorder()
	clHandler.HandleSubmitCL(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when check run is running, got %d", rec.Code)
	}

	// 3. Submit with failed check run without justification -> blocked
	postRun("failed")
	rec = httptest.NewRecorder()
	clHandler.HandleSubmitCL(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when check run is failed, got %d", rec.Code)
	}

	// 4. Submit with failed check run with override justification -> succeeds (303)
	overrideReq := httptest.NewRequest("POST", "/cl/1/submit", strings.NewReader("justification=Urgent+hotfix+approved+by+eng+VP"))
	overrideReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	overrideReq.SetPathValue("id", "1")
	rec = httptest.NewRecorder()
	clHandler.HandleSubmitCL(rec, overrideReq)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 See Other with valid justification override, got %d: %s", rec.Code, rec.Body.String())
	}

	// 5. Reset and test clean passing check run
	ResetDemoStoresForTest()
	postRun("passed")
	rec = httptest.NewRecorder()
	clHandler.HandleSubmitCL(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 See Other when check run is passed, got %d: %s", rec.Code, rec.Body.String())
	}
}
