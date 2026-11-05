-- Notification delivery history: one append-only row per attempt to deliver
-- an alert event to a channel, written by shared/notifications/deliverylog
-- from the alerter (the synchronous send, or `queued` when it hands the event
-- to the delivery workers) and from the delivery worker (each attempt, and
-- the events it drops). Rows go through the connection pool, never the
-- notification claim transaction, so a failed send that rolls its claim back
-- still leaves its row.
--
-- event_type is free text (created, reminder, resolved, acknowledged,
-- suspected_cause, and whatever event the alerter claims next). error is
-- already truncated and stripped of URLs and credentials by the writer.
-- channel_name is a snapshot, so a renamed channel keeps its history
-- readable. recipients_* are set only by fan-out channels (SMS).
--
-- Rows go with their alert, channel or workspace; the scheduler's retention
-- cleanup prunes them after deliverylog.RetentionDays (90).
CREATE TABLE alert_notification_deliveries (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id            UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    alert_id             UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    channel_id           UUID NOT NULL REFERENCES alert_channels(id) ON DELETE CASCADE,
    channel_name         TEXT NOT NULL,
    event_type           TEXT NOT NULL,
    delivery_key         TEXT NOT NULL,
    attempt              INT NOT NULL CHECK (attempt >= 1),
    path                 TEXT NOT NULL CHECK (path IN ('sync', 'async')),
    outcome              TEXT NOT NULL CHECK (outcome IN (
                             'queued', 'delivered', 'failed', 'permanent_failure',
                             'dropped_superseded', 'dropped_ack')),
    error                TEXT,
    recipients_total     INT,
    recipients_delivered INT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_alert_notification_deliveries_alert
    ON alert_notification_deliveries(alert_id, created_at);
CREATE INDEX idx_alert_notification_deliveries_channel
    ON alert_notification_deliveries(channel_id, created_at DESC);
-- Retention prunes per tenant by age.
CREATE INDEX idx_alert_notification_deliveries_tenant_created
    ON alert_notification_deliveries(tenant_id, created_at);
