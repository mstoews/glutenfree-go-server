-- Stores domain: wards (reference data), stores, menu_items.

CREATE TABLE wards (
    id      serial PRIMARY KEY,
    name_ja text NOT NULL,
    name_en text NOT NULL
);

-- Tokyo 23 special wards (reference data). Mirrors the static wards.json
-- shipped in the app bundle; also served at GET /wards.
INSERT INTO wards (name_ja, name_en) VALUES
    ('千代田区', 'Chiyoda'),
    ('中央区',   'Chuo'),
    ('港区',     'Minato'),
    ('新宿区',   'Shinjuku'),
    ('文京区',   'Bunkyo'),
    ('台東区',   'Taito'),
    ('墨田区',   'Sumida'),
    ('江東区',   'Koto'),
    ('品川区',   'Shinagawa'),
    ('目黒区',   'Meguro'),
    ('大田区',   'Ota'),
    ('世田谷区', 'Setagaya'),
    ('渋谷区',   'Shibuya'),
    ('中野区',   'Nakano'),
    ('杉並区',   'Suginami'),
    ('豊島区',   'Toshima'),
    ('北区',     'Kita'),
    ('荒川区',   'Arakawa'),
    ('板橋区',   'Itabashi'),
    ('練馬区',   'Nerima'),
    ('足立区',   'Adachi'),
    ('葛飾区',   'Katsushika'),
    ('江戸川区', 'Edogawa');

CREATE TYPE store_status AS ENUM ('draft', 'pending', 'approved', 'rejected');

CREATE TABLE stores (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    ward_id          int NOT NULL REFERENCES wards (id),
    name             text NOT NULL,
    address          text NOT NULL,
    latitude         double precision NOT NULL,
    longitude        double precision NOT NULL,
    is_gf_oriented   boolean NOT NULL DEFAULT false,
    opening_hours    jsonb NOT NULL DEFAULT '[]'::jsonb,  -- [{day,open,close}], day 0=Sunday, HHMM strings
    status           store_status NOT NULL DEFAULT 'draft',
    rejection_reason text,
    approved_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- Browse-by-ward and keyset pagination over approved stores.
CREATE INDEX stores_ward_status_idx ON stores (ward_id, status);
CREATE INDEX stores_status_id_idx ON stores (status, id);

CREATE TYPE gf_status AS ENUM ('certified', 'on_request', 'contains_hidden_gluten');

CREATE TABLE menu_items (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    store_id     uuid NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
    name         text NOT NULL,
    price_yen    int NOT NULL,
    image_url    text,
    gf_status    gf_status NOT NULL,
    gf_note      text,
    sort_order   int NOT NULL DEFAULT 0,
    is_available boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX menu_items_store_idx ON menu_items (store_id, sort_order);
