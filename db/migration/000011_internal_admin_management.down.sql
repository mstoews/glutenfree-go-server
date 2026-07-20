DROP TABLE IF EXISTS internal_password_resets;

ALTER TABLE internal_admins DROP COLUMN IF EXISTS updated_at;
ALTER TABLE internal_admins DROP COLUMN IF EXISTS name;
