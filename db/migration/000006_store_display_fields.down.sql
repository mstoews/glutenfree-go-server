ALTER TABLE stores
    DROP COLUMN IF EXISTS photo_url,
    DROP COLUMN IF EXISTS gf_status,
    DROP COLUMN IF EXISTS blurb,
    DROP COLUMN IF EXISTS nearest_station,
    DROP COLUMN IF EXISTS review_count,
    DROP COLUMN IF EXISTS rating,
    DROP COLUMN IF EXISTS price_level,
    DROP COLUMN IF EXISTS cuisine;
