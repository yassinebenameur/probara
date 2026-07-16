-- AI-generated root cause analyses for incidents. One row per analysis run
-- (history is kept), populated asynchronously by the worker. The deterministic
-- dependency-graph root cause lives on alerts (migration 000051); this table
-- holds the richer LLM diagnosis over the incident's probe evidence.
CREATE TABLE incident_ai_analyses (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    incident_id          UUID NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    status               TEXT NOT NULL CHECK (status IN ('pending', 'ready', 'failed')),
    model                TEXT,
    summary              TEXT,
    probable_root_cause  TEXT,
    contributing_factors JSONB,
    recommended_actions  JSONB,
    confidence           TEXT,
    evidence             JSONB,
    error_message        TEXT,
    requested_by         UUID,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at         TIMESTAMPTZ
);

-- "Latest analysis for this incident" is the hot read path.
CREATE INDEX idx_incident_ai_analyses_incident_created
    ON incident_ai_analyses (incident_id, created_at DESC);
