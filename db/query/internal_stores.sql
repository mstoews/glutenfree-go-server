-- Internal-operator store CRUD (/internal/stores). Unlike the store-admin
-- self-serve queries in admin_stores.sql, these let an operator write every
-- column on any store -- including the curated display fields (rating,
-- review_count) and an explicit status -- so back-office staff can seed and
-- correct restaurant entries directly.

-- name: CreateStoreFull :one
-- Operator create: populate every field, including an explicit status (e.g.
-- seed a fully-curated store straight to 'approved').
INSERT INTO stores (
    ward_id, name, address, latitude, longitude, is_gf_oriented, opening_hours,
    status, cuisine, price_level, rating, review_count, nearest_station, blurb,
    gf_status, photo_url, name_en, phone, source_url, notes
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13, $14,
    $15, $16, $17, $18, $19, $20
)
RETURNING *;

-- name: UpdateStoreFull :one
-- Operator edit: overwrite every editable field on any store, regardless of
-- its current status. status is passed explicitly so operators can move a
-- store between states as part of an edit.
UPDATE stores
SET ward_id         = $2,
    name            = $3,
    address         = $4,
    latitude        = $5,
    longitude       = $6,
    is_gf_oriented  = $7,
    opening_hours   = $8,
    status          = $9,
    -- Keep approved_at meaningful when an operator approves from the form
    -- rather than the Approve button: stamp it on the transition into
    -- 'approved', preserve the original date across later edits, and clear it
    -- if the store leaves 'approved'. Invariant: approved_at is set iff the
    -- store is currently approved.
    approved_at     = CASE
                          WHEN $9::store_status = 'approved' THEN COALESCE(approved_at, now())
                          ELSE NULL
                      END,
    cuisine         = $10,
    price_level     = $11,
    rating          = $12,
    review_count    = $13,
    nearest_station = $14,
    blurb           = $15,
    gf_status       = $16,
    photo_url       = $17,
    name_en         = $18,
    phone           = $19,
    source_url      = $20,
    notes           = $21,
    updated_at      = now()
WHERE id = $1
RETURNING *;

-- name: DeleteStore :execrows
-- Hard delete. menu_items and store_admins cascade via their FK ON DELETE
-- CASCADE, so this removes the store and everything hanging off it.
DELETE FROM stores WHERE id = $1;

-- name: CountStoresByNameWard :one
-- Dedup helper for imports: is there already a store with this name in the ward?
-- Makes re-running an import (or the future scraper) idempotent.
SELECT count(*) FROM stores WHERE lower(name) = lower($1) AND ward_id = $2;

-- name: ListStoresMissingCoords :many
-- Geocode backfill queue: stores with an address but no coordinates yet
-- (imports land at 0,0 because the CSV carries no lat/lng).
SELECT * FROM stores
WHERE latitude = 0 AND longitude = 0 AND address <> ''
ORDER BY created_at
LIMIT $1;

-- name: UpdateStoreCoords :execrows
UPDATE stores SET latitude = $2, longitude = $3, updated_at = now()
WHERE id = $1;
