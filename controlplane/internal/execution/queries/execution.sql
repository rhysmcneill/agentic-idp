-- name: CreateRun :one
INSERT INTO runs (
    tenant_id, actor_id, environment_id, pipeline_id, tier, status,
    ci_provider, idempotency_key, diff_ref
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
RETURNING *;

-- name: GetRun :one
SELECT * FROM runs WHERE tenant_id = $1 AND id = $2;

-- name: UpdateRunStatus :one
UPDATE runs
SET status = sqlc.arg(new_status)::text,
    started_at = CASE WHEN sqlc.arg(new_status)::text = 'executing' AND started_at IS NULL THEN now() ELSE started_at END,
    finished_at = CASE WHEN sqlc.arg(new_status)::text IN ('succeeded', 'failed', 'cancelled', 'timed_out') THEN now() ELSE finished_at END
WHERE id = sqlc.arg(id) AND status = sqlc.arg(old_status)
RETURNING *;

-- name: CreateApproval :one
INSERT INTO approvals (run_id) VALUES ($1) RETURNING *;

-- name: GetApprovalByRunID :one
SELECT * FROM approvals WHERE run_id = $1;

-- name: DecideApproval :one
UPDATE approvals
SET decision = sqlc.arg(decision), decided_at = now(), approver_actor_id = sqlc.arg(approver_actor_id)
WHERE run_id = sqlc.arg(run_id) AND decision IS NULL
RETURNING *;

-- name: ClaimNextExecutingRun :one
WITH next AS (
    SELECT id FROM runs
    WHERE status = 'executing' AND claimed_by IS NULL AND environment_id = ANY(sqlc.arg(environment_ids)::uuid[])
    ORDER BY requested_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE runs
SET claimed_by = sqlc.arg(claimed_by), claimed_at = now()
WHERE id = (SELECT id FROM next)
RETURNING *;

-- name: ReportRunResult :one
-- Only a worker that still holds the claim (status = 'executing', claimed_by
-- matches) may report a result — mirrors verification.Complete's guard so a
-- stolen worker credential can't forge results for another worker's claim.
UPDATE runs
SET status = sqlc.arg(new_status)::text,
    ci_external_ref = sqlc.arg(ci_external_ref),
    ci_url = sqlc.arg(ci_url),
    ci_raw_status = sqlc.arg(ci_raw_status),
    finished_at = CASE WHEN sqlc.arg(new_status)::text IN ('succeeded', 'failed', 'cancelled', 'timed_out') THEN now() ELSE finished_at END
WHERE id = sqlc.arg(id) AND claimed_by = sqlc.arg(claimed_by) AND status = 'executing'
RETURNING *;

-- name: FindUnresolvedExecutingRuns :many
SELECT r.* FROM runs r
JOIN pipelines p ON p.id = r.pipeline_id
WHERE r.status = 'executing'
  AND r.ci_external_ref IS NULL
  AND r.environment_id = ANY(sqlc.arg(environment_ids)::uuid[])
  AND p.provider = sqlc.arg(provider)
  AND (p.settings->>'repo')::text = sqlc.arg(repo)::text
  AND p.workflow_ref = sqlc.arg(workflow_ref)
ORDER BY r.requested_at;

-- name: ResolveRunExternalRef :one
UPDATE runs
SET ci_external_ref = sqlc.arg(ci_external_ref), ci_url = sqlc.arg(ci_url)
WHERE id = sqlc.arg(id) AND ci_external_ref IS NULL
RETURNING *;
