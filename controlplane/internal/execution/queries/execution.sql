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
