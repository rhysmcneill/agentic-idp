-- name: CreateActor :one
INSERT INTO actors (tenant_id, type, name, team_id, trust_tier, authorized_by, expires_at, idempotency_key)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetActor :one
SELECT * FROM actors WHERE id = $1;

-- name: GetRootActor :one
SELECT * FROM actors WHERE tenant_id = $1 AND authorized_by IS NULL;

-- name: GetActorByIdempotencyKey :one
SELECT * FROM actors WHERE tenant_id = $1 AND authorized_by = $2 AND idempotency_key = $3;

-- name: GrantActorEnvironment :exec
INSERT INTO actor_environments (actor_id, environment_id) VALUES ($1, $2);

-- name: ListActorEnvironments :many
SELECT environment_id FROM actor_environments WHERE actor_id = $1;
