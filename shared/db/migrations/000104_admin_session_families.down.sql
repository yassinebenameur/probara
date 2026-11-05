DROP TRIGGER IF EXISTS admin_sessions_default_family ON admin_sessions;
DROP FUNCTION IF EXISTS admin_sessions_default_family();
DROP INDEX IF EXISTS idx_admin_sessions_family_id;
ALTER TABLE admin_sessions DROP CONSTRAINT IF EXISTS admin_sessions_family_id_fkey;
ALTER TABLE admin_sessions DROP COLUMN IF EXISTS family_id;
DROP TABLE IF EXISTS admin_session_families;
