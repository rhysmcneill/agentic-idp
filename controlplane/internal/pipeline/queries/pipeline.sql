-- name: CreatePipeline :one
INSERT INTO pipelines (tenant_id, environment_id, provider, workflow_ref, settings, mutating)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: GetPipeline :one
SELECT * FROM pipelines WHERE id = $1 AND tenant_id = $2;
