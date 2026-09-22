-- name: CreateActor :one
INSERT INTO actors (tenant_id, type, name, team_id, trust_tier, authorized_by, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetActor :one
SELECT * FROM actors WHERE id = $1;
