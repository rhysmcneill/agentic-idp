-- name: GetTelemetryInstance :one
SELECT * FROM telemetry_instance LIMIT 1;

-- name: CreateTelemetryInstance :one
INSERT INTO telemetry_instance DEFAULT VALUES RETURNING *;

-- name: CountActiveAgents :one
SELECT COUNT(*)::bigint FROM actors WHERE type = 'agent' AND status = 'active';

-- name: CountRunsSince :one
SELECT COUNT(*)::bigint FROM runs WHERE requested_at >= $1;
