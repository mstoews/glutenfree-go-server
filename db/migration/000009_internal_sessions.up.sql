-- Refresh sessions for internal-operator logins (/internal/auth/*). Kept
-- separate from the app-user `sessions` table because those FK to users(id);
-- internal admins live in internal_admins. Mirrors the sessions shape so the
-- refresh + revocation flow works the same way.
CREATE TABLE internal_sessions (
    id            uuid PRIMARY KEY,                                        -- equals the refresh token's jti
    admin_id      uuid NOT NULL REFERENCES internal_admins (id) ON DELETE CASCADE,
    refresh_token text NOT NULL,
    user_agent    text NOT NULL DEFAULT '',
    client_ip     text NOT NULL DEFAULT '',
    is_blocked    boolean NOT NULL DEFAULT false,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX internal_sessions_admin_id_idx ON internal_sessions (admin_id);
