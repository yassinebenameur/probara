-- Per-tenant LLM configuration for AI root cause analysis. One row per tenant.
-- The api_key is stored encrypted (shared/secrets envelope) and never returned
-- to clients. When a tenant has no enabled row, the worker/API fall back to the
-- LLM_* environment defaults.
CREATE TABLE ai_settings (
    tenant_id         UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    enabled           BOOLEAN NOT NULL DEFAULT FALSE,
    provider          TEXT NOT NULL DEFAULT 'openai_compat',
    base_url          TEXT NOT NULL DEFAULT '',
    api_key_encrypted TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL DEFAULT '',
    json_mode         TEXT NOT NULL DEFAULT 'off',
    max_tokens        INTEGER NOT NULL DEFAULT 1024,
    timeout_seconds   INTEGER NOT NULL DEFAULT 60,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
