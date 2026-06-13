-- name: ListAvailableMenuItems :many
SELECT * FROM menu_items
WHERE store_id = $1 AND is_available = true
ORDER BY sort_order, id;
