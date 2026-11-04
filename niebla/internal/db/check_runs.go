package db

import (
	"context"
	"fmt"
	"time"
)

// CheckRun represents a CI runner execution status against a specific CL patchset.
type CheckRun struct {
	ID             int64      `json:"id"`
	CLID           int64      `json:"cl_id"`
	PatchsetNumber int        `json:"patchset_number"`
	RunnerName     string     `json:"runner_name"`
	Provider       string     `json:"provider"`
	Status         string     `json:"status"` // queued, running, passed, failed, canceled, cancelled, skipped
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

	query := `
		INSERT INTO cl_check_runs (
			cl_id, patchset_number, runner_name, provider, status, external_url, log_blob_sha256, started_at, completed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE($8, now()), $9)
		ON CONFLICT (cl_id, patchset_number, runner_name) DO UPDATE SET
			provider = EXCLUDED.provider,
			status = EXCLUDED.status,
			external_url = COALESCE(EXCLUDED.external_url, cl_check_runs.external_url),
			log_blob_sha256 = COALESCE(EXCLUDED.log_blob_sha256, cl_check_runs.log_blob_sha256),
			started_at = COALESCE(EXCLUDED.started_at, cl_check_runs.started_at),
			completed_at = COALESCE(EXCLUDED.completed_at, cl_check_runs.completed_at)
		RETURNING id, cl_id, patchset_number, runner_name, provider, status, external_url, log_blob_sha256, started_at, completed_at, created_at;
	`

	var out CheckRun
	var extURL *string
	if run.ExternalURL != "" {
		extURL = &run.ExternalURL
	}
	err := d.pool.QueryRow(ctx, query,
		run.CLID,
		run.PatchsetNumber,
		run.RunnerName,
		run.Provider,
		run.Status,
		extURL,
		run.LogBlobSHA256,
		run.StartedAt,
		run.CompletedAt,
	).Scan(
		&out.ID,
		&out.CLID,
		&out.PatchsetNumber,
		&out.RunnerName,
		&out.Provider,
		&out.Status,
		&extURL,
		&out.LogBlobSHA256,
		&out.StartedAt,
		&out.CompletedAt,
		&out.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert check run: %w", err)
	}
	if extURL != nil {
		out.ExternalURL = *extURL
	}
	return &out, nil
}

// ListCheckRunsForPatchset retrieves all check runs for a CL patchset.
func (d *DB) ListCheckRunsForPatchset(ctx context.Context, clID int64, patchsetNumber int) ([]CheckRun, error) {
	query := `
		SELECT id, cl_id, patchset_number, runner_name, provider, status, external_url, log_blob_sha256, started_at, completed_at, created_at
		FROM cl_check_runs
		WHERE cl_id = $1 AND patchset_number = $2
		ORDER BY runner_name ASC;
	`
	rows, err := d.pool.Query(ctx, query, clID, patchsetNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to query check runs for CL %d PS %d: %w", clID, patchsetNumber, err)
	}
	defer rows.Close()

	var runs []CheckRun
	for rows.Next() {
		var r CheckRun
		var extURL *string
		if err := rows.Scan(
			&r.ID,
			&r.CLID,
			&r.PatchsetNumber,
			&r.RunnerName,
			&r.Provider,
			&r.Status,
			&extURL,
			&r.LogBlobSHA256,
			&r.StartedAt,
			&r.CompletedAt,
			&r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan check run: %w", err)
		}
		if extURL != nil {
			r.ExternalURL = *extURL
		}
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading check runs: %w", err)
	}
	if runs == nil {
		runs = []CheckRun{}
	}
	return runs, nil
}

// GetLatestPatchsetNumber returns the highest patchset number for a changelist (defaults to 1).
func (d *DB) GetLatestPatchsetNumber(ctx context.Context, clID int64) (int, error) {
	query := `
		SELECT COALESCE(MAX(patchset_number), 1)
		FROM cl_patchsets
		WHERE cl_id = $1;
	`
	var latestNum int
	if err := d.pool.QueryRow(ctx, query, clID).Scan(&latestNum); err != nil {
		return 1, fmt.Errorf("failed to get latest patchset number for CL %d: %w", clID, err)
	}
	if latestNum < 1 {
		latestNum = 1
	}
	return latestNum, nil
}

// AreCheckRunsGreen determines if all check runs for a CL patchset have completed successfully.
// Returns:
//
//	green: true if total > 0 and pending == 0 and failed == 0.
//	total: total number of check runs recorded for this patchset.
//	pending: count of runs currently in 'queued' or 'running'.
//	failed: count of runs in 'failed', 'canceled', or 'cancelled'.
func (d *DB) AreCheckRunsGreen(ctx context.Context, clID int64, patchsetNumber int) (green bool, total int, pending int, failed int, err error) {
	runs, err := d.ListCheckRunsForPatchset(ctx, clID, patchsetNumber)
	if err != nil {
		return false, 0, 0, 0, err
	}
	total = len(runs)
	if total == 0 {
		return false, 0, 0, 0, nil
	}
	for _, run := range runs {
		switch run.Status {
		case "queued", "running":
			pending++
		case "failed", "canceled", "cancelled":
			failed++
		case "passed", "skipped":
			// green states
		default:
			failed++
		}
	}
	green = (total > 0 && pending == 0 && failed == 0)
	return green, total, pending, failed, nil
}
