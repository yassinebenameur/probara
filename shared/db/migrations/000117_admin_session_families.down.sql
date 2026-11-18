DROP TRIGGER IF EXISTS admin_sessions_default_family ON admin_sessions;
DROP FUNCTION IF EXISTS admin_sessions_default_family();

-- Tokens of revoked families must not come back to life once the family
-- table is gone: the old scheme only knows the token row's own revoked_at.
UPDATE admin_sessions s
SET revoked_at = COALESCE(f.revoked_at, NOW())
FROM admin_session_families f
WHERE f.id = s.family_id AND f.revoked_at IS NOT NULL AND s.revoked_at IS NULL;

DROP INDEX IF EXISTS idx_admin_sessions_family_id;
ALTER TABLE admin_sessions DROP COLUMN IF EXISTS family_id;
DROP TABLE IF EXISTS admin_session_families;

DROP INDEX IF EXISTS idx_admin_sessions_refresh_token_hash;
CREATE INDEX idx_admin_sessions_refresh_token_hash ON admin_sessions(refresh_token_hash) WHERE revoked_at IS NULL;
