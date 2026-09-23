-- name: CreateTenant :one
INSERT INTO tenants (name) VALUES ($1) RETURNING *;

-- name: GetTenant :one
SELECT * FROM tenants WHERE id = $1;

-- name: AnyTenantExists :one
SELECT EXISTS (SELECT 1 FROM tenants) AS exists;
