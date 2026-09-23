-- name: CreateTeam :one
INSERT INTO teams (tenant_id, name) VALUES ($1, $2) RETURNING *;

-- name: GetTeam :one
SELECT * FROM teams WHERE id = $1;

-- name: GetTeamByName :one
SELECT * FROM teams WHERE tenant_id = $1 AND name = $2;
