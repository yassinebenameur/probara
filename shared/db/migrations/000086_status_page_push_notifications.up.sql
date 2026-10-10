-- 000086_status_page_push_notifications.up.sql
-- Web Push notifications for public status page visitors.
--
-- Two tables and one index:
--
--   status_page_push_subscriptions -- one row per (page, browser endpoint).
--   status_page_push_deliveries    -- the claim ledger that makes fan-out
--                                     exactly-once across status-page replicas.
--   idx_monitor_state_intervals_notifiable -- supports the reconciler's scan.
--
-- The trigger source is monitor_state_intervals (migration 000082), not the
-- statuspage.updates NATS subject: push/agent/OTLP monitors publish
-- "check_result" and never "state_change", and core NATS has no redelivery.
-- The timeline is written inside every transition's own transaction, so it is
-- the only complete log -- and its UUID primary key is a per-transition
-- identity written exactly once, which is what the ledger keys on.

CREATE TABLE status_page_push_subscriptions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status_page_id  UUID NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    -- The push service endpoint minted by the browser. Bounded by CHECK
    -- rather than hashed so ON CONFLICT can use a plain column target; real
    -- endpoints are ~200 bytes, well under btree's ~2704-byte row limit.
    endpoint        TEXT NOT NULL,
    p256dh          TEXT NOT NULL,
    auth            TEXT NOT NULL,
    user_agent      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Refreshed by an idempotent re-subscribe the page fires at most once a
    -- day; drives the stale-subscription sweep.
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_success_at TIMESTAMPTZ,
    failure_count   INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT status_page_push_endpoint_https CHECK (endpoint LIKE 'https://%'),
    CONSTRAINT status_page_push_endpoint_len   CHECK (char_length(endpoint) BETWEEN 20 AND 2048)
);

-- Per (page, endpoint), not per endpoint: one browser subscribed to two pages
-- on the same origin presents the same endpoint twice.
CREATE UNIQUE INDEX idx_sp_push_subs_page_endpoint
    ON status_page_push_subscriptions(status_page_id, endpoint);
CREATE INDEX idx_sp_push_subs_stale
    ON status_page_push_subscriptions(last_seen_at);

CREATE TABLE status_page_push_deliveries (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    status_page_id UUID NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    monitor_id     UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    -- monitor_state_intervals.id. Deliberately no FK: intervals are subject to
    -- retention, and a purged interval must not cascade away the record that
    -- we already notified about it.
    interval_id    UUID NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('down', 'recovered')),
    -- Snapshot of the label the page showed (status_page_section_monitors
    -- .display_name, else monitors.name). The notification must name the
    -- monitor as the visitor saw it, even if it is renamed later.
    monitor_name   TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
    claimed_at     TIMESTAMPTZ,
    sent_at        TIMESTAMPTZ,
    sent_count     INTEGER,
    failed_count   INTEGER,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The exactly-once key. Both replicas run the identical detector INSERT; the
-- unique index makes the loser a no-op, so no leader election or advisory
-- lock is needed.
CREATE UNIQUE INDEX idx_sp_push_deliveries_dedup
    ON status_page_push_deliveries(status_page_id, interval_id);
CREATE INDEX idx_sp_push_deliveries_pending
    ON status_page_push_deliveries(created_at)
    WHERE status IN ('pending', 'sending');

-- The reconciler scans recent notifiable intervals across all monitors.
-- idx_monitor_state_intervals_window is (monitor_id, started_at) and cannot
-- serve that; this partial index keeps the scan proportional to real
-- transitions rather than to the whole timeline.
CREATE INDEX idx_monitor_state_intervals_notifiable
    ON monitor_state_intervals(created_at)
    WHERE state IN ('down', 'up');
