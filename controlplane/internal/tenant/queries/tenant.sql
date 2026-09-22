-- name: CreateTenant :one
INSERT INTO tenants (name) VALUES ($1) RETURNING *;

-- name: GetTenant :one
SELECT * FROM tenants WHERE id = $1;
