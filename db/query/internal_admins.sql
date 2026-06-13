-- name: CreateInternalAdmin :one
INSERT INTO internal_admins (email, password_hash)
VALUES ($1, $2)
RETURNING *;

-- name: GetInternalAdminByEmail :one
SELECT * FROM internal_admins WHERE lower(email) = lower($1);
