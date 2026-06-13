-- name: CreateUser :one
INSERT INTO users (email, password_hash, apple_user_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower($1);

-- name: GetUserByAppleID :one
SELECT * FROM users WHERE apple_user_id = $1;

-- name: UpdateSubscription :one
UPDATE users
SET subscription_status = $2,
    sub_expires_at      = $3,
    updated_at          = now()
WHERE id = $1
RETURNING *;
