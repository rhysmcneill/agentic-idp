-- name: CreateLocalUser :one
INSERT INTO local_users (actor_id, username, password_hash) VALUES ($1, $2, $3) RETURNING *;

-- name: GetLocalUserByUsername :one
SELECT * FROM local_users WHERE username = $1;
