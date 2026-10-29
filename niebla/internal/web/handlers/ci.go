package handlers

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"niebla/internal/db"
)

var (
	demoCheckRunsMu    sync.RWMutex
	demoCheckRunsStore = make(map[string][]db.CheckRun) // key: "<clID>:<patchsetNumber>"
)

// CIHandler handles CI check run reporting and status inspection endpoints.
type CIHandler struct {
	db *db.DB
}

// NewCIHandler creates a new CIHandler instance.
func NewCIHandler(database *db.DB) *CIHandler {
	return &CIHandler{
		db: database,
	}
}

// ReportStatusRequest represents the JSON body sent by runners reporting a check status.
type ReportStatusRequest struct {
	CLID           int64      `json:"cl_id"`
	PatchsetNumber int        `json:"patchset_number"`
	RunnerName     string     `json:"runner_name"`
	Provider       string     `json:"provider"`
	Status         string     `json:"status"` // queued, running, passed, failed, canceled, skipped
	ExternalURL    string     `json:"external_url,omitempty"`
	LogBlobSHA256  string     `json:"log_blob_sha256,omitempty"` // Hex-encoded 64-char SHA256
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// HandlePostCheckRunStatus receives and records check run statuses from CI runners.
// POST /api/v1/ci/status
func (h *CIHandler) HandlePostCheckRunStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ReportStatusRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON payload: %v", err), http.StatusBadRequest)
		return
	}

	if req.CLID <= 0 {
		http.Error(w, "valid cl_id is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.RunnerName) == "" {
		http.Error(w, "runner_name is required", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Provider) == "" {
		req.Provider = "local"
	}
	validStatuses := map[string]bool{
		"queued": true, "running": true, "passed": true,
		"failed": true, "canceled": true, "cancelled": true, "skipped": true,
	}
	statusLower := strings.ToLower(strings.TrimSpace(req.Status))
	if !validStatuses[statusLower] {
		http.Error(w, fmt.Sprintf("invalid status %q: must be one of queued, running, passed, failed, canceled, skipped", req.Status), http.StatusBadRequest)
		return
	}
	req.Status = statusLower

	var logHashBytes []byte
	if req.LogBlobSHA256 != "" {
		b, err := hex.DecodeString(strings.TrimSpace(req.LogBlobSHA256))
		if err != nil || len(b) != 32 {
			http.Error(w, "log_blob_sha256 must be a valid 64-character hex string", http.StatusBadRequest)
			return
		}
		logHashBytes = b
	}

	patchsetNum := req.PatchsetNumber
	if patchsetNum <= 0 {
		if h.db != nil {
			latest, err := h.db.GetLatestPatchsetNumber(r.Context(), req.CLID)
			if err == nil && latest > 0 {
				patchsetNum = latest
			} else {
				patchsetNum = 1
			}
		} else {
			patchsetNum = 1
		}
	}

	runInput := db.CheckRun{
		CLID:           req.CLID,
		PatchsetNumber: patchsetNum,
		RunnerName:     strings.TrimSpace(req.RunnerName),
		Provider:       strings.TrimSpace(req.Provider),
		Status:         req.Status,
		ExternalURL:    strings.TrimSpace(req.ExternalURL),
		LogBlobSHA256:  logHashBytes,
		StartedAt:      req.StartedAt,
		CompletedAt:    req.CompletedAt,
	}

	var savedRun *db.CheckRun
	if h.db != nil {
		out, err := h.db.UpsertCheckRun(r.Context(), runInput)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to save check run: %v", err), http.StatusInternalServerError)
			return
		}
		savedRun = out
	} else {
		// In-memory demo store fallback
		demoCheckRunsMu.Lock()
		key := fmt.Sprintf("%d:%d", req.CLID, patchsetNum)
		current := demoCheckRunsStore[key]
		updated := false
		for i, cr := range current {
			if cr.RunnerName == runInput.RunnerName {
				runInput.ID = cr.ID
				runInput.CreatedAt = cr.CreatedAt
				current[i] = runInput
				updated = true
				break
			}
		}
		if !updated {
			runInput.ID = int64(len(current) + 1)
			runInput.CreatedAt = time.Now().UTC()
			current = append(current, runInput)
		}
		demoCheckRunsStore[key] = current
		savedRun = &runInput
		demoCheckRunsMu.Unlock()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":    "ok",
		"check_run": savedRun,
	})
}

// HandleGetCheckRuns lists check runs and calculates aggregate green status for a patchset.
// GET /api/v1/ci/status?cl_id=12&patchset=1
// or GET /cl/{id}/checks
func (h *CIHandler) HandleGetCheckRuns(w http.ResponseWriter, r *http.Request) {
	var clID int64
	var err error

	// Try path param {id} if available
	pathID := r.PathValue("id")
	if pathID != "" {
		clID, err = strconv.ParseInt(pathID, 10, 64)
	} else {
		// Query param cl_id
		qID := r.URL.Query().Get("cl_id")
		if qID != "" {
			clID, err = strconv.ParseInt(qID, 10, 64)
		}
	}
	if err != nil || clID <= 0 {
		http.Error(w, "valid cl_id is required", http.StatusBadRequest)
		return
	}

	patchsetNum := 0
	qPS := r.URL.Query().Get("patchset")
	if qPS != "" {
		if p, err := strconv.Atoi(qPS); err == nil && p > 0 {
			patchsetNum = p
		}
	}

	if patchsetNum <= 0 {
		if h.db != nil {
			latest, err := h.db.GetLatestPatchsetNumber(r.Context(), clID)
			if err == nil && latest > 0 {
				patchsetNum = latest
			} else {
				patchsetNum = 1
			}
		} else {
			patchsetNum = 1
		}
	}

	var runs []db.CheckRun
	var green bool
	var total, pending, failed int

	if h.db != nil {
		g, tot, pend, fail, err := h.db.AreCheckRunsGreen(r.Context(), clID, patchsetNum)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get check runs: %v", err), http.StatusInternalServerError)
			return
		}
		green = g
		total = tot
		pending = pend
		failed = fail

		rList, err := h.db.ListCheckRunsForPatchset(r.Context(), clID, patchsetNum)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to list check runs: %v", err), http.StatusInternalServerError)
			return
		}
		runs = rList
	} else {
		demoCheckRunsMu.RLock()
		key := fmt.Sprintf("%d:%d", clID, patchsetNum)
		runs = demoCheckRunsStore[key]
		demoCheckRunsMu.RUnlock()

		total = len(runs)
		for _, r := range runs {
			switch r.Status {
			case "queued", "running":
				pending++
			case "failed", "canceled", "cancelled":
				failed++
			case "passed", "skipped":
				// ok
			default:
				failed++
			}
		}
		green = (total > 0 && pending == 0 && failed == 0)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":          "ok",
		"cl_id":           clID,
		"patchset_number": patchsetNum,
		"green":           green,
		"total":           total,
		"pending":         pending,
		"failed":          failed,
		"check_runs":      runs,
	})
}
