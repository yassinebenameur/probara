-- Notification delivery history: one append-only row per attempt to deliver
-- an alert event to a channel (per recipient on fan-out channels), and per
-- queued event the delivery worker drops. Written only through
-- shared/notifications/deliverylog, by the alerter's synchronous send, the
-- delivery worker, and the API's channel test sends (is_test, no alert).
--
-- outcome is delivered, failed (transient: retried), permanent_failure (not
-- retried) or dropped; reason says why a row was dropped or failed (Go
-- constants deliverylog.Reason*, free text so a new cause needs no
-- migration). error is sanitized by the writer: URLs reduced to their host,
-- credentials and the channel's secret values masked, 512 characters at most.
-- recipient is masked and recipient_hash is the SHA-256 the recipient ledger
-- (alert_notification_recipient_deliveries) keys on; both stay NULL for a
-- channel that does not fan out.
--
-- Rows go with their alert, channel or workspace; the scheduler's retention
-- cleanup deletes rows older than deliverylog.RetentionDays (90) for every
-- workspace, whatever its data_retention_days.
CREATE TABLE alert_notification_deliveries (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    alert_id        UUID REFERENCES alerts(id) ON DELETE CASCADE,
    channel_id      UUID NOT NULL REFERENCES alert_channels(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,
    delivery_key    TEXT NOT NULL DEFAULT '',
    recipient       TEXT,
    recipient_hash  TEXT,
    attempt         INT NOT NULL CHECK (attempt >= 1),
    path            TEXT NOT NULL CHECK (path IN ('sync', 'async')),
    is_test         BOOLEAN NOT NULL DEFAULT FALSE,
    outcome         TEXT NOT NULL CHECK (outcome IN ('delivered', 'failed', 'permanent_failure', 'dropped')),
    reason          TEXT,
    error           TEXT,
    provider_status INT,
    duration_ms     INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT alert_notification_deliveries_alert_or_test CHECK (alert_id IS NOT NULL OR is_test)
);

-- An alert's attempts in order.
CREATE INDEX idx_alert_notification_deliveries_alert
    ON alert_notification_deliveries(alert_id, created_at)
    WHERE alert_id IS NOT NULL;
-- A channel's recent attempts, unfiltered.
CREATE INDEX idx_alert_notification_deliveries_channel
    ON alert_notification_deliveries(channel_id, created_at DESC);
-- A channel's attempts by outcome: the outcome filter, and the last success
-- and failure behind the failing flag.
CREATE INDEX idx_alert_notification_deliveries_channel_outcome
    ON alert_notification_deliveries(channel_id, outcome, created_at DESC);
-- Retention prunes per workspace by age.
CREATE INDEX idx_alert_notification_deliveries_tenant_created
    ON alert_notification_deliveries(tenant_id, created_at);
