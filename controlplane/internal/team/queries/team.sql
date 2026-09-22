-- name: CreateTeam :one
INSERT INTO teams (tenant_id, name) VALUES ($1, $2) RETURNING *;

-- name: GetTeam :one
SELECT * FROM teams WHERE id = $1;
