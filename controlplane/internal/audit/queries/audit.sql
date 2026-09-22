-- name: CreateAuditEvent :one
INSERT INTO audit_events (tenant_id, actor_id, action, run_id, tier, environment_id, ttl_seconds, metadata)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING *;

-- name: GetAuditEvent :one
SELECT * FROM audit_events WHERE id = $1;
