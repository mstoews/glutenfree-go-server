-- Display fields for the Gurufuri rich store cards + detail (design handoff).
-- All defaulted/nullable so existing rows stay valid.
ALTER TABLE stores
    ADD COLUMN cuisine         text      NOT NULL DEFAULT '',
    ADD COLUMN price_level     int       NOT NULL DEFAULT 2,            -- 1..3 (¥ .. ¥¥¥)
    ADD COLUMN rating          real      NOT NULL DEFAULT 0,           -- 0 = unrated
    ADD COLUMN review_count    int       NOT NULL DEFAULT 0,
    ADD COLUMN nearest_station text      NOT NULL DEFAULT '',
    ADD COLUMN blurb           text      NOT NULL DEFAULT '',
    ADD COLUMN gf_status       gf_status NOT NULL DEFAULT 'on_request', -- store-level GF assurance
    ADD COLUMN photo_url       text;                                    -- nullable CDN URL (image pipeline TBD)
