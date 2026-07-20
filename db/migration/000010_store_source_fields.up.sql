-- Provenance + contact fields captured from the curated / web-scraped
-- restaurant CSV import (name_en, phone, source_url, notes). All defaulted so
-- existing rows stay valid.
ALTER TABLE stores
    ADD COLUMN name_en    text NOT NULL DEFAULT '',
    ADD COLUMN phone      text NOT NULL DEFAULT '',
    ADD COLUMN source_url text NOT NULL DEFAULT '',
    ADD COLUMN notes      text NOT NULL DEFAULT '';
