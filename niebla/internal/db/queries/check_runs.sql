-- name: UpsertCheckRun :one
INSERT INTO cl_check_runs (
    cl_id, patchset_number, runner_name, provider, status, external_url, log_blob_sha256, started_at, completed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, COALESCE(sqlc.arg(started_at)::timestamptz, now()), sqlc.arg(completed_at)::timestamptz)
ON CONFLICT (cl_id, patchset_number, runner_name) DO UPDATE SET
    provider = EXCLUDED.provider,
    status = EXCLUDED.status,
    external_url = COALESCE(EXCLUDED.external_url, cl_check_runs.external_url),
    log_blob_sha256 = COALESCE(EXCLUDED.log_blob_sha256, cl_check_runs.log_blob_sha256),
    started_at = COALESCE(EXCLUDED.started_at, cl_check_runs.started_at),
    completed_at = COALESCE(EXCLUDED.completed_at, cl_check_runs.completed_at)
RETURNING id, cl_id, patchset_number, runner_name, provider, status, external_url, log_blob_sha256, started_at, completed_at, created_at;

-- name: ListCheckRunsForPatchset :many
SELECT id, cl_id, patchset_number, runner_name, provider, status, external_url, log_blob_sha256, started_at, completed_at, created_at
FROM cl_check_runs
WHERE cl_id = $1 AND patchset_number = $2
ORDER BY runner_name ASC;
