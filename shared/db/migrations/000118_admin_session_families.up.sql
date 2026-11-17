-- Session families (api/internal/services/adminauth/sessions.go). A family is
-- one sign-in, what the account page lists as a session: the refresh token
-- rotates inside it (admin_sessions keeps one row per token), and the access
-- JWT carries its id as the `sid` claim. revoked_at on the family is the one
-- authority for "is this sign-in still valid": the refresh lookup and the
-- auth middleware both read it, so revoking that row ends the refresh chain
-- and every access JWT of the family on their next request.
CREATE TABLE admin_session_families (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    admin_user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    -- NULL for sign-ins made before this migration (method unknown).
    auth_method TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Updated at sign-in and at each refresh, never per request.
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- The newest refresh token's expiry; refresh slides it forward.
    expires_at TIMESTAMPTZ NOT NULL,
    -- Client address (shared/clientip) and user agent of the last sign-in or
    -- refresh.
    ip TEXT,
    user_agent TEXT,
    revoked_at TIMESTAMPTZ,
    revoke_reason TEXT,
    CONSTRAINT admin_session_families_auth_method_check
        CHECK (auth_method IS NULL OR auth_method IN ('password', 'oidc'))
);

CREATE INDEX idx_admin_session_families_user_live
    ON admin_session_families(admin_user_id) WHERE revoked_at IS NULL;

ALTER TABLE admin_sessions
    ADD COLUMN family_id UUID REFERENCES admin_session_families(id) ON DELETE CASCADE;

CREATE INDEX idx_admin_sessions_family_id ON admin_sessions(family_id);

-- Refresh-token reuse detection looks a token up after it was rotated, so
-- the lookup index must cover revoked rows too.
DROP INDEX IF EXISTS idx_admin_sessions_refresh_token_hash;
CREATE INDEX idx_admin_sessions_refresh_token_hash ON admin_sessions(refresh_token_hash);

-- Live refresh tokens become one family each, keyed by the token row's id,
-- so signed-in users stay signed in: their next refresh continues that family
-- and mints an access JWT carrying its sid. Rotated, revoked and expired rows
-- keep family_id NULL; no refresh path accepts a token without a family, and
-- rotated rows of the old scheme carried no lineage to group them by.
INSERT INTO admin_session_families (id, admin_user_id, created_at, last_used_at, expires_at, ip, user_agent)
SELECT id, admin_user_id, created_at, COALESCE(last_used_at, created_at), expires_at, ip, user_agent
FROM admin_sessions
WHERE revoked_at IS NULL AND expires_at > NOW();

UPDATE admin_sessions s
SET family_id = s.id
FROM admin_session_families f
WHERE f.id = s.id;

-- An API binary older than this migration (rolling deploy) inserts tokens
-- without a family. Give each such row its own family rather than leave it
-- unusable and sign the user out.
CREATE FUNCTION admin_sessions_default_family() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.family_id IS NULL AND NEW.revoked_at IS NULL THEN
        INSERT INTO admin_session_families (id, admin_user_id, created_at, last_used_at, expires_at, ip, user_agent)
        VALUES (NEW.id, NEW.admin_user_id, COALESCE(NEW.created_at, NOW()), COALESCE(NEW.created_at, NOW()),
                NEW.expires_at, NEW.ip, NEW.user_agent);
        NEW.family_id := NEW.id;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER admin_sessions_default_family
    BEFORE INSERT ON admin_sessions
    FOR EACH ROW EXECUTE FUNCTION admin_sessions_default_family();
