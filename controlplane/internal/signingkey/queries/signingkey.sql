-- name: GetSigningKey :one
SELECT * FROM signing_keys WHERE id = true;

-- name: CreateSigningKey :one
INSERT INTO signing_keys (private_key) VALUES ($1) RETURNING *;
