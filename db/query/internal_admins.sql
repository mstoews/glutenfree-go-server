-- name: CreateInternalAdmin :one
INSERT INTO internal_admins (email, password_hash, name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetInternalAdminByEmail :one
SELECT * FROM internal_admins WHERE lower(email) = lower($1);

-- name: GetInternalAdminByID :one
SELECT * FROM internal_admins WHERE id = $1;

-- name: ListInternalAdmins :many
SELECT * FROM internal_admins ORDER BY lower(email);

-- name: UpdateInternalAdminPassword :one
UPDATE internal_admins
SET password_hash = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- ---- password resets ----

-- name: CreateInternalPasswordReset :one
INSERT INTO internal_password_resets (admin_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetInternalPasswordResetByTokenHash :one
SELECT * FROM internal_password_resets WHERE token_hash = $1;

-- name: MarkInternalPasswordResetUsed :execrows
-- Single-use: the WHERE guard makes a concurrent second redemption a no-op
-- (0 rows affected), so the handler can detect and reject it.
UPDATE internal_password_resets
SET used_at = now()
WHERE id = $1 AND used_at IS NULL;

-- name: DeleteInternalPasswordResetsForAdmin :execrows
-- Invalidate any outstanding reset links, e.g. after a successful password change.
DELETE FROM internal_password_resets WHERE admin_id = $1;
