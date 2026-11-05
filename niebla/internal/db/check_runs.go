package db

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"niebla/internal/db/store"
)

// CheckRun represents a single presubmit CI check or runner status record for a patchset.
type CheckRun struct {
	ID             int64      `json:"id"`
	CLID           int64      `json:"cl_id"`
	PatchsetNumber int        `json:"patchset_number"`
	RunnerName     string     `json:"runner_name"`
	Provider       string     `json:"provider"`
	Status         string     `json:"status"` // "queued", "running", "passed", "failed", "canceled", "cancelled", "skipped"
	ExternalURL    string     `json:"external_url,omitempty"`
	LogBlobSHA256  []byte     `json:"log_blob_sha256,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// UpsertCheckRun creates or updates a check run record for a specific (cl_id, patchset_number, runner_name).
func (d *DB) UpsertCheckRun(ctx context.Context, run CheckRun) (*CheckRun, error) {
	if run.CLID <= 0 {
		return nil, fmt.Errorf("cl_id must be positive")
	}
	if run.PatchsetNumber <= 0 {
		run.PatchsetNumber = 1
	}
	if run.RunnerName == "" {
		return nil, fmt.Errorf("runner_name cannot be empty")
	}
	if run.Provider == "" {
		run.Provider = "local"
	}
	if run.Status == "" {
		run.Status = "queued"
	}

	// Auto-set timestamps if omitted
	now := time.Now().UTC()
	if run.StartedAt == nil && run.Status != "queued" {
		run.StartedAt = &now
	}
	isTerminal := run.Status == "passed" || run.Status == "failed" || run.Status == "canceled" || run.Status == "cancelled" || run.Status == "skipped"
	if isTerminal && run.CompletedAt == nil {
		run.CompletedAt = &now
	}

	var extURL pgtype.Text
	if run.ExternalURL != "" {
		extURL = pgtype.Text{String: run.ExternalURL, Valid: true}
	}
	var startedAt pgtype.Timestamptz
	if run.StartedAt != nil {
		startedAt = pgtype.Timestamptz{Time: *run.StartedAt, Valid: true}
	}
	var completedAt pgtype.Timestamptz
	if run.CompletedAt != nil {
		completedAt = pgtype.Timestamptz{Time: *run.CompletedAt, Valid: true}
	}

	row, err := d.queries.UpsertCheckRun(ctx, store.UpsertCheckRunParams{
		ClID:           run.CLID,
		PatchsetNumber: int32(run.PatchsetNumber),
		RunnerName:     run.RunnerName,
		Provider:       run.Provider,
		Status:         run.Status,
		ExternalUrl:    extURL,
		LogBlobSha256:  run.LogBlobSHA256,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upsert check run: %w", err)
	}
	out := toCheckRun(row)
	return &out, nil
}

// ListCheckRunsForPatchset retrieves all check runs for a CL patchset.
func (d *DB) ListCheckRunsForPatchset(ctx context.Context, clID int64, patchsetNumber int) ([]CheckRun, error) {
	rows, err := d.queries.ListCheckRunsForPatchset(ctx, store.ListCheckRunsForPatchsetParams{
		ClID:           clID,
		PatchsetNumber: int32(patchsetNumber),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query check runs for CL %d PS %d: %w", clID, patchsetNumber, err)
	}

	runs := make([]CheckRun, len(rows))
	for i, r := range rows {
		runs[i] = toCheckRun(r)
	}
	return runs, nil
}

// EvaluateCheckRuns aggregates status counts and returns whether all check runs passed.
func EvaluateCheckRuns(runs []CheckRun) (green bool, total int, pending int, failed int) {
	total = len(runs)
	for _, run := range runs {
		switch run.Status {
		case "queued", "running":
			pending++
		case "failed", "canceled", "cancelled":
			failed++
		}
	}
	green = (total > 0) && (pending == 0) && (failed == 0)
	return green, total, pending, failed
}

// FindMissingRequiredRunners returns runner names that are required by policy but not green.
func FindMissingRequiredRunners(runs []CheckRun, requiredRunners []string) []string {
	if len(requiredRunners) == 0 {
		return nil
	}
	greenRunners := make(map[string]bool)
	for _, run := range runs {
		if run.Status == "passed" || run.Status == "skipped" {
			greenRunners[strings.ToLower(strings.TrimSpace(run.RunnerName))] = true
		}
	}
	var missing []string
	for _, required := range requiredRunners {
		if !greenRunners[strings.ToLower(strings.TrimSpace(required))] {
			missing = append(missing, required)
		}
	}
	return missing
}

// GetLatestPatchsetNumber returns the highest patchset number for a changelist (defaults to 1).
func (d *DB) GetLatestPatchsetNumber(ctx context.Context, clID int64) (int, error) {
	latestNum, err := d.queries.GetLatestPatchsetNumberForCL(ctx, clID)
	if err != nil {
		return 1, fmt.Errorf("failed to get latest patchset number for CL %d: %w", clID, err)
	}
	if latestNum < 1 {
		latestNum = 1
	}
	return int(latestNum), nil
}

// AreCheckRunsGreen determines if all check runs for a CL patchset have completed successfully.
func (d *DB) AreCheckRunsGreen(ctx context.Context, clID int64, patchsetNumber int) (green bool, total int, pending int, failed int, err error) {
	runs, err := d.ListCheckRunsForPatchset(ctx, clID, patchsetNumber)
	if err != nil {
		return false, 0, 0, 0, err
	}
	green, total, pending, failed = EvaluateCheckRuns(runs)
	return green, total, pending, failed, nil
}
