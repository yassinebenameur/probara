-- Append-only audit trail. BIGSERIAL (not UUID): keyset-pagination friendly
-- and half the index size for a table that only ever grows.
CREATE TABLE audit_log (
    id BIGSERIAL PRIMARY KEY,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    tenant_id UUID,                    -- NULL for platform-level events (login, user CRUD)
    actor_type TEXT NOT NULL CHECK (actor_type IN ('admin_user', 'api_key', 'anonymous')),
    actor_id UUID,                     -- admin_users.id or api_keys.id; NULL for failed logins
    actor_label TEXT NOT NULL DEFAULT '',  -- username / key name snapshot (survives deletes)
    action TEXT NOT NULL,              -- e.g. 'auth.login', 'monitor.update', 'apikey.revoke'
    resource_type TEXT NOT NULL DEFAULT '',
    resource_id TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL CHECK (outcome IN ('success', 'failure', 'denied')),
    status_code INT,
    ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    details JSONB
);

CREATE INDEX idx_audit_log_tenant_time ON audit_log (tenant_id, occurred_at DESC);
CREATE INDEX idx_audit_log_tenant_actor ON audit_log (tenant_id, actor_id, occurred_at DESC);
CREATE INDEX idx_audit_log_tenant_action ON audit_log (tenant_id, action, occurred_at DESC);
CREATE INDEX idx_audit_log_time ON audit_log (occurred_at);
