-- Session families: one row per sign-in. Every refresh-token rotation stays
-- in its family, the access JWT carries the family id as its `sid` claim,
-- and revoking the family row ends both the refresh chain and every access
-- JWT issued for it on the next request.
CREATE TABLE admin_session_families (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    admin_user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    auth_method TEXT NOT NULL DEFAULT 'password',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ,
    revoke_reason TEXT,
    CONSTRAINT admin_session_families_auth_method_check CHECK (auth_method IN ('password', 'oidc'))
);

CREATE INDEX idx_admin_session_families_live
    ON admin_session_families(admin_user_id) WHERE revoked_at IS NULL;

-- Existing rows have no lineage: each becomes its own family.
INSERT INTO admin_session_families (id, admin_user_id, created_at, revoked_at, revoke_reason)
SELECT id, admin_user_id, created_at, revoked_at,
       CASE WHEN revoked_at IS NOT NULL THEN 'legacy' END
FROM admin_sessions;

ALTER TABLE admin_sessions ADD COLUMN family_id UUID;
UPDATE admin_sessions SET family_id = id;
ALTER TABLE admin_sessions
    ALTER COLUMN family_id SET NOT NULL,
    ADD CONSTRAINT admin_sessions_family_id_fkey
        FOREIGN KEY (family_id) REFERENCES admin_session_families(id) ON DELETE CASCADE;

CREATE INDEX idx_admin_sessions_family_id ON admin_sessions(family_id);

-- An API binary older than this migration (rolling deploy) inserts sessions
-- without a family; give each such row its own family instead of failing
-- the NOT NULL and logging the user out.
CREATE FUNCTION admin_sessions_default_family() RETURNS trigger AS $$
BEGIN
    IF NEW.family_id IS NULL THEN
        INSERT INTO admin_session_families (id, admin_user_id, created_at)
        VALUES (NEW.id, NEW.admin_user_id, COALESCE(NEW.created_at, NOW()));
        NEW.family_id := NEW.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER admin_sessions_default_family
    BEFORE INSERT ON admin_sessions
    FOR EACH ROW EXECUTE FUNCTION admin_sessions_default_family();
