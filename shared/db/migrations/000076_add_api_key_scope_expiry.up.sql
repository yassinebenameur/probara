-- Scoped API keys. DEFAULT 'write' backfills existing keys with their current
-- full-access behavior. expires_at NULL = never expires.
ALTER TABLE api_keys
    ADD COLUMN scope TEXT NOT NULL DEFAULT 'write' CHECK (scope IN ('read', 'write')),
    ADD COLUMN expires_at TIMESTAMPTZ,
    ADD COLUMN created_by UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    ADD COLUMN last_used_at TIMESTAMPTZ;
