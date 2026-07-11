DROP INDEX IF EXISTS idx_admin_users_external_identity;
DROP INDEX IF EXISTS idx_admin_users_email;

ALTER TABLE admin_users
    DROP COLUMN IF EXISTS platform_role,
    DROP COLUMN IF EXISTS external_subject,
    DROP COLUMN IF EXISTS external_issuer,
    DROP COLUMN IF EXISTS email;

-- Restore NOT NULL; OIDC-only users (NULL hash) block this, so give them an
-- unusable placeholder first. bcrypt of a random value is not required — a
-- non-bcrypt string never matches ComparePassword.
UPDATE admin_users SET password_hash = '!disabled-by-migration-rollback' WHERE password_hash IS NULL;
ALTER TABLE admin_users
    ALTER COLUMN password_hash SET NOT NULL;
