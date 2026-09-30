-- name: CreateWorkerCredential :one
INSERT INTO worker_credentials (tenant_id, name, token_hash) VALUES ($1, $2, $3) RETURNING *;

-- name: GetWorkerCredentialByTokenHash :one
SELECT * FROM worker_credentials WHERE token_hash = $1 AND revoked_at IS NULL;

-- name: GetWorkerCredential :one
SELECT * FROM worker_credentials WHERE id = $1;

-- name: ListWorkerCredentials :many
SELECT * FROM worker_credentials WHERE tenant_id = $1 ORDER BY name;

-- name: GetWorkerCredentialByTenantAndName :one
SELECT * FROM worker_credentials WHERE tenant_id = $1 AND name = $2 AND revoked_at IS NULL;

-- name: RotateWorkerCredentialToken :one
UPDATE worker_credentials SET token_hash = $2 WHERE id = $1 RETURNING *;

-- name: GrantWorkerCredentialEnvironment :exec
INSERT INTO worker_credential_environments (worker_credential_id, environment_id)
VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListWorkerCredentialEnvironments :many
SELECT environment_id FROM worker_credential_environments WHERE worker_credential_id = $1;

-- name: SetWorkerCICallbackURL :one
UPDATE worker_credentials SET ci_callback_url = $2 WHERE id = $1 RETURNING *;

-- name: GetCICallbackURLForRepo :one
-- Scoped to workers actually granted an environment that has a pipeline
-- registered for repo+provider — a worker credential for one environment
-- must not be able to answer discovery for a repo it has no relationship to.
SELECT wc.ci_callback_url FROM worker_credentials wc
JOIN worker_credential_environments wce ON wce.worker_credential_id = wc.id
JOIN pipelines p ON p.environment_id = wce.environment_id
WHERE wc.ci_callback_url IS NOT NULL
  AND wc.revoked_at IS NULL
  AND p.provider = sqlc.arg(provider)
  AND (p.settings->>'repo')::text = sqlc.arg(repo)::text
ORDER BY wc.created_at DESC
LIMIT 1;
