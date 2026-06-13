-- name: CreateStore :one
INSERT INTO stores (ward_id, name, address, latitude, longitude, is_gf_oriented, opening_hours, status)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'draft')
RETURNING *;

-- name: GetStoreByID :one
SELECT * FROM stores WHERE id = $1;

-- name: UpdateStoreProfile :one
UPDATE stores
SET name           = $2,
    address        = $3,
    latitude       = $4,
    longitude      = $5,
    is_gf_oriented = $6,
    opening_hours  = $7,
    updated_at     = now()
WHERE id = $1
RETURNING *;

-- name: SubmitStore :one
-- First submit or resubmit after rejection -> back to the review queue.
UPDATE stores
SET status = 'pending', rejection_reason = NULL, updated_at = now()
WHERE id = $1 AND status IN ('draft', 'rejected')
RETURNING *;

-- name: ListStoresByStatus :many
SELECT s.*, w.name_ja AS ward_name_ja, w.name_en AS ward_name_en
FROM stores s
JOIN wards w ON w.id = s.ward_id
WHERE s.status = $1
ORDER BY s.created_at DESC;

-- name: ApproveStore :one
UPDATE stores
SET status = 'approved', approved_at = now(), rejection_reason = NULL, updated_at = now()
WHERE id = $1 AND status = 'pending'
RETURNING *;

-- name: RejectStore :one
UPDATE stores
SET status = 'rejected', rejection_reason = $2, updated_at = now()
WHERE id = $1 AND status = 'pending'
RETURNING *;
