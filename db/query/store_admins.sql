-- name: CreateStoreAdmin :one
INSERT INTO store_admins (store_id, email, password_hash)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetStoreAdminByEmail :one
SELECT * FROM store_admins WHERE lower(email) = lower($1);

-- name: GetStoreAdminByID :one
SELECT * FROM store_admins WHERE id = $1;
