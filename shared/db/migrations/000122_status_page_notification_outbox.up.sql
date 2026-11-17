-- The status-page notification outbox: one row per page and event that
-- subscribers hear about. Every row is written in the same transaction as
-- the change it announces (an incident published to the page or resolved, a
-- public update, a maintenance window scheduled), by the API
-- (api/internal/services/pagenotify), or by the API runner's sweeps (a
-- maintenance occurrence starting, an outage published automatically).
--
-- Two consumers read it:
--   * the API runner (api/internal/services/pagesubscribers) plans each
--     pending row once (FOR UPDATE SKIP LOCKED): it renders the content and
--     adds one delivery per confirmed subscriber in one transaction, then
--     mails them with the same claimed/sending states as SLA report emails;
--   * the status-page push reconciler turns recent incident rows into
--     visitor push deliveries (status_page_push_deliveries).
CREATE TABLE status_page_notification_outbox (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id             UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    status_page_id        UUID NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    event_kind            TEXT NOT NULL CHECK (event_kind IN (
                              'incident_published', 'incident_updated', 'incident_resolved',
                              'maintenance_scheduled', 'maintenance_started')),
    incident_id           UUID REFERENCES incidents(id) ON DELETE CASCADE,
    -- The public update an incident_updated row announces.
    timeline_entry_id     UUID REFERENCES incident_timeline_entries(id) ON DELETE CASCADE,
    maintenance_window_id UUID REFERENCES maintenance_windows(id) ON DELETE CASCADE,
    -- The occurrence a maintenance_started row announces.
    occurrence_starts_at  TIMESTAMPTZ,
    -- Identifies the event within (page, kind), so a sweep that runs on
    -- every API replica inserts each event once (ON CONFLICT DO NOTHING).
    dedupe_key            TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'planned', 'skipped')),
    -- What the mail says, rendered when planned (pagesubscribers.notice).
    content               JSONB,
    recipients            INTEGER NOT NULL DEFAULT 0,
    skip_reason           TEXT,
    planned_at            TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT status_page_notification_outbox_subject CHECK (
        (event_kind LIKE 'incident\_%' AND incident_id IS NOT NULL AND maintenance_window_id IS NULL)
        OR (event_kind LIKE 'maintenance\_%' AND maintenance_window_id IS NOT NULL AND incident_id IS NULL)),
    CONSTRAINT status_page_notification_outbox_dedupe UNIQUE (status_page_id, event_kind, dedupe_key)
);

CREATE INDEX idx_sp_notification_outbox_pending
    ON status_page_notification_outbox (created_at) WHERE status = 'pending';
CREATE INDEX idx_sp_notification_outbox_recent
    ON status_page_notification_outbox (created_at);
CREATE INDEX idx_sp_notification_outbox_tenant ON status_page_notification_outbox (tenant_id);
CREATE INDEX idx_sp_notification_outbox_incident
    ON status_page_notification_outbox (incident_id) WHERE incident_id IS NOT NULL;
CREATE INDEX idx_sp_notification_outbox_window
    ON status_page_notification_outbox (maintenance_window_id) WHERE maintenance_window_id IS NOT NULL;

-- One subscriber's copy of one outbox row. A delivery is reserved
-- ('claimed', safe to hand back) and becomes 'sending', committed, only
-- immediately before its message goes out, so 'uncertain' (may have
-- arrived, never resent) only ever follows a send that started. Removing a
-- subscriber deletes what was still waiting for them.
CREATE TABLE status_page_notification_deliveries (
    outbox_id       UUID NOT NULL REFERENCES status_page_notification_outbox(id) ON DELETE CASCADE,
    subscriber_id   UUID NOT NULL REFERENCES status_page_subscribers(id) ON DELETE CASCADE,
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'claimed', 'sending', 'sent', 'failed', 'uncertain', 'skipped')),
    -- Started sends; a claim handed back unsent does not count.
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    claim_id        UUID,
    claimed_at      TIMESTAMPTZ,
    sent_at         TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (outbox_id, subscriber_id)
);

CREATE INDEX idx_sp_notification_deliveries_due
    ON status_page_notification_deliveries (outbox_id, next_attempt_at) WHERE status = 'pending';
CREATE INDEX idx_sp_notification_deliveries_held
    ON status_page_notification_deliveries (claimed_at) WHERE status IN ('claimed', 'sending');
CREATE INDEX idx_sp_notification_deliveries_subscriber
    ON status_page_notification_deliveries (subscriber_id);
CREATE INDEX idx_sp_notification_deliveries_tenant ON status_page_notification_deliveries (tenant_id);
