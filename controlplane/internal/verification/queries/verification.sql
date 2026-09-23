-- name: CreateVerification :one
INSERT INTO environment_verifications (tenant_id, environment_id, requested_by)
VALUES ($1, $2, $3) RETURNING *;

-- name: GetVerification :one
SELECT * FROM environment_verifications WHERE id = $1;

-- name: ClaimNextPendingVerification :one
WITH next AS (
    SELECT id FROM environment_verifications
    WHERE status = 'pending' AND claimed_by IS NULL AND environment_id = ANY(sqlc.arg(environment_ids)::uuid[])
    ORDER BY requested_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE environment_verifications
SET claimed_by = sqlc.arg(claimed_by), claimed_at = now()
WHERE id = (SELECT id FROM next)
RETURNING *;

-- name: CompleteVerification :one
UPDATE environment_verifications
SET status = $2, tier_results = $3, completed_at = now()
WHERE id = $1 AND claimed_by = $4
RETURNING *;
