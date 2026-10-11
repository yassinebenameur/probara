-- Automatic, display-only triage of open alerts (worker/internal/airca relay +
-- triage consumer). One row per alert; never read by the alerter.
CREATE TABLE alert_triage (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id         UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    alert_id          UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    monitor_id        UUID,   -- copied from the alert at claim; NULL for mesh_edge. No FK: the alert FK already cascades purges
    status            TEXT NOT NULL CHECK (status IN ('pending','ready','failed','skipped')),
    skip_reason       TEXT CHECK (skip_reason IN ('resolved','cooldown','not_modeled','disabled')),
    enqueued_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- last publish; drives republish
    attempts          INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0), -- publishes so far; a failed publish is undone
    started_at        TIMESTAMPTZ,  -- first delivery that found the row pending; NULL = still queued. Starts the run timeout
    hypothesis        TEXT,   -- catalogue rca key (ai.Metadata.Hypothesis), ready only
    confidence        TEXT CHECK (confidence IN ('low','medium','high')),
    summary           TEXT,
    metadata          JSONB,  -- ai.Metadata, same shape as incident_ai_analyses.metadata
    decision_snapshot JSONB,  -- ai.DecisionSnapshot; NULL when no decision was sent and on failure/skip
    error_message     TEXT,   -- fixed operator strings only, never error text
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),  -- claim time = evidence request time
    completed_at      TIMESTAMPTZ,
    CONSTRAINT alert_triage_alert_unique UNIQUE (alert_id),
    CONSTRAINT alert_triage_skip_reason CHECK ((status = 'skipped') = (skip_reason IS NOT NULL)),
    CONSTRAINT alert_triage_completed CHECK ((status = 'pending') = (completed_at IS NULL))
);
CREATE INDEX idx_alert_triage_tenant_created ON alert_triage (tenant_id, created_at);           -- hourly cap
CREATE INDEX idx_alert_triage_pending ON alert_triage (enqueued_at) WHERE status = 'pending';   -- relay republish/fail
CREATE INDEX idx_alert_triage_monitor_ready ON alert_triage (tenant_id, monitor_id, completed_at DESC)
    WHERE status = 'ready';                                                                    -- cooldown

ALTER TABLE ai_settings
    ADD COLUMN auto_triage_enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN triage_delay_seconds    INTEGER NOT NULL DEFAULT 60
        CONSTRAINT ai_settings_triage_delay_range CHECK (triage_delay_seconds BETWEEN 0 AND 3600),
    ADD COLUMN triage_cooldown_seconds INTEGER NOT NULL DEFAULT 900
        CONSTRAINT ai_settings_triage_cooldown_range CHECK (triage_cooldown_seconds BETWEEN 0 AND 86400),
    ADD COLUMN triage_max_per_hour     INTEGER NOT NULL DEFAULT 60
        CONSTRAINT ai_settings_triage_max_range CHECK (triage_max_per_hour BETWEEN 1 AND 600);
