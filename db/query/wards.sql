-- name: ListWards :many
SELECT * FROM wards ORDER BY id;

-- name: GetWard :one
SELECT * FROM wards WHERE id = $1;
