-- GlutenFree initial schema: users + auth sessions.
-- gen_random_uuid() is built into Postgres 13+ (no extension needed).

CREATE TYPE subscription_status AS ENUM ('free', 'active', 'expired', 'revoked');

CREATE TABLE users (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email               text NOT NULL,
    password_hash       text,                                   -- null for Apple-only accounts
    apple_user_id       text,
    subscription_status subscription_status NOT NULL DEFAULT 'free',
    sub_expires_at      timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

-- Case-insensitive unique email; Apple id unique when present.
CREATE UNIQUE INDEX users_email_lower_key ON users (lower(email));
CREATE UNIQUE INDEX users_apple_user_id_key ON users (apple_user_id) WHERE apple_user_id IS NOT NULL;

CREATE TABLE sessions (
    id            uuid PRIMARY KEY,                             -- equals the refresh token's jti
    user_id       uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    refresh_token text NOT NULL,
    user_agent    text NOT NULL DEFAULT '',
    client_ip     text NOT NULL DEFAULT '',
    is_blocked    boolean NOT NULL DEFAULT false,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
