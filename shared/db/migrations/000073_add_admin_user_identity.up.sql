-- Identity groundwork for RBAC + OIDC SSO.
-- password_hash becomes nullable: OIDC-only users have no local password.
ALTER TABLE admin_users
    ALTER COLUMN password_hash DROP NOT NULL;

ALTER TABLE admin_users
    ADD COLUMN email TEXT,
    ADD COLUMN external_issuer TEXT,
    ADD COLUMN external_subject TEXT,
    ADD COLUMN platform_role TEXT NOT NULL DEFAULT 'member'
        CHECK (platform_role IN ('superadmin', 'member'));

-- Every pre-existing admin was a global superuser; preserve that behavior.
UPDATE admin_users SET platform_role = 'superadmin';

CREATE UNIQUE INDEX idx_admin_users_email
    ON admin_users (lower(email)) WHERE email IS NOT NULL;

CREATE UNIQUE INDEX idx_admin_users_external_identity
    ON admin_users (external_issuer, external_subject)
    WHERE external_subject IS NOT NULL;
