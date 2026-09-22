-- name: RevokeSession :one
INSERT INTO revoked_sessions (jti, actor_id, expires_at) VALUES ($1, $2, $3) RETURNING *;

-- name: IsRevoked :one
SELECT
    EXISTS (SELECT 1 FROM actors WHERE id = $1 AND status = 'revoked')
    OR EXISTS (SELECT 1 FROM revoked_sessions WHERE jti = $2)
    AS is_revoked;

-- name: DeleteExpiredSessions :execrows
DELETE FROM revoked_sessions WHERE expires_at < now();
