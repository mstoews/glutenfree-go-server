-- Operator-admin self-management: a display name, an updated_at touched on
-- password changes, and a table backing the emailed password-reset flow.

ALTER TABLE internal_admins ADD COLUMN name text NOT NULL DEFAULT '';
ALTER TABLE internal_admins ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- Password-reset grants for /internal/auth/forgot-password. Only the SHA-256 of
-- the token is stored, so a database leak does not yield usable reset links.
-- Rows are single-use (used_at) and short-lived (expires_at).
CREATE TABLE internal_password_resets (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id   uuid NOT NULL REFERENCES internal_admins (id) ON DELETE CASCADE,
    token_hash text NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX internal_password_resets_token_hash_key ON internal_password_resets (token_hash);
CREATE INDEX internal_password_resets_admin_idx ON internal_password_resets (admin_id);
