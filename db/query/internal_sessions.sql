-- name: CreateInternalSession :one
INSERT INTO internal_sessions (id, admin_id, refresh_token, user_agent, client_ip, is_blocked, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetInternalSession :one
SELECT * FROM internal_sessions WHERE id = $1;

-- name: DeleteInternalSession :execrows
-- Revoke a single refresh session (logout).
DELETE FROM internal_sessions WHERE id = $1;

-- name: DeleteInternalSessionsForAdmin :execrows
-- Revoke every refresh session for an admin, used after a password change or
-- reset so sessions opened with the old password cannot be renewed.
DELETE FROM internal_sessions WHERE admin_id = $1;
