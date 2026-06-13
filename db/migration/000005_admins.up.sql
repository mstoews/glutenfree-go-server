-- Store admins (one account per store, manage via /admin/*) and internal ops
-- admins (review queue via /internal/*).

CREATE TABLE store_admins (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    store_id      uuid NOT NULL REFERENCES stores (id) ON DELETE CASCADE,
    email         text NOT NULL,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX store_admins_email_lower_key ON store_admins (lower(email));
CREATE INDEX store_admins_store_idx ON store_admins (store_id);

CREATE TABLE internal_admins (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email         text NOT NULL,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX internal_admins_email_lower_key ON internal_admins (lower(email));
