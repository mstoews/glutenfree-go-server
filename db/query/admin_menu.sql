-- name: CreateMenuItem :one
INSERT INTO menu_items (store_id, name, price_yen, image_url, gf_status, gf_note, sort_order, is_available)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: ListMenuItemsByStore :many
SELECT * FROM menu_items WHERE store_id = $1 ORDER BY sort_order, id;

-- name: UpdateMenuItem :one
UPDATE menu_items
SET name         = $3,
    price_yen    = $4,
    image_url    = $5,
    gf_status    = $6,
    gf_note      = $7,
    sort_order   = $8,
    is_available = $9,
    updated_at   = now()
WHERE id = $1 AND store_id = $2
RETURNING *;

-- name: DeleteMenuItem :execrows
DELETE FROM menu_items WHERE id = $1 AND store_id = $2;
