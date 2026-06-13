-- name: ListApprovedStores :many
-- Keyset pagination over approved stores, ordered by id. Pass the zero UUID
-- (00000000-...) as @cursor for the first page; it is less than every real id,
-- so `id > @cursor` returns from the start. Optional ward filter via @ward_id.
SELECT s.*, w.name_ja AS ward_name_ja, w.name_en AS ward_name_en
FROM stores s
JOIN wards w ON w.id = s.ward_id
WHERE s.status = 'approved'
  AND (sqlc.narg(ward_id)::int IS NULL OR s.ward_id = sqlc.narg(ward_id))
  AND s.id > sqlc.arg(cursor)
ORDER BY s.id
LIMIT sqlc.arg(page_limit);

-- name: GetApprovedStore :one
SELECT s.*, w.name_ja AS ward_name_ja, w.name_en AS ward_name_en
FROM stores s
JOIN wards w ON w.id = s.ward_id
WHERE s.id = $1 AND s.status = 'approved';
