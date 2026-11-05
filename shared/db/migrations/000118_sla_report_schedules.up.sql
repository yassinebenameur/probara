-- Scheduled emailed SLA reports (api/internal/services/reportschedules).
-- The API's runner claims due rows FOR UPDATE SKIP LOCKED, so any number of
-- API replicas send each closed period once.
CREATE TABLE sla_report_schedules (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    sla_id          UUID NOT NULL REFERENCES slas(id) ON DELETE CASCADE,
    -- Bare addresses, lower-cased; one message per recipient.
    recipients      TEXT[] NOT NULL CHECK (cardinality(recipients) BETWEEN 1 AND 50),
    formats         TEXT[] NOT NULL DEFAULT '{pdf}'
                    CHECK (cardinality(formats) >= 1 AND formats <@ ARRAY['pdf', 'csv']::TEXT[]),
    -- Freeze the closed period through IssueReport before sending; off
    -- sends the issued copy when one exists, else the live report.
    auto_issue      BOOLEAN NOT NULL DEFAULT TRUE,
    enabled         BOOLEAN NOT NULL DEFAULT TRUE,
    -- The current period's close plus the grace the runner waits for late
    -- results; recomputed after every run and on (re-)enable.
    next_run_at     TIMESTAMPTZ NOT NULL,
    last_run_at     TIMESTAMPTZ,
    last_period_key TEXT,
    last_sent_count INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,
    -- HMAC key of the per-recipient unsubscribe links (never returned by the
    -- API). Deleting the schedule revokes every link it mailed.
    unsubscribe_key BYTEA NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sla_report_schedules_due ON sla_report_schedules (next_run_at) WHERE enabled;
CREATE INDEX idx_sla_report_schedules_sla ON sla_report_schedules (tenant_id, sla_id);

-- Recipients who followed an unsubscribe link. Keyed by a SHA-256 of the
-- lower-cased address, so the opt-out outlives an edit of the recipient list
-- without keeping the address twice.
CREATE TABLE report_unsubscribes (
    schedule_id     UUID NOT NULL REFERENCES sla_report_schedules(id) ON DELETE CASCADE,
    email_hash      TEXT NOT NULL,
    unsubscribed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (schedule_id, email_hash)
);

-- Reports a schedule issued name it as their issuer.
ALTER TABLE sla_reports ADD COLUMN issued_by_schedule BOOLEAN NOT NULL DEFAULT FALSE;
