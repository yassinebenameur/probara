-- Per-recipient delivery progress of one notification event, written by
-- channel plugins that fan out to several recipients (the SMS plugin) through
-- plugin.DispatchRequest.Recipients (shared/notifications/recipientledger).
-- A retry of a partially delivered event — the alerter's next cycle on any
-- replica, or a JetStream redelivery to any worker — reads it back and skips
-- the recipients the provider already accepted.
--
-- event_key is notifications.RecipientEventKey: the event type, plus the
-- round for reminders. recipient_hash is the SHA-256 of the recipient address,
-- so the table does not keep a second copy of phone numbers. Rows go with
-- their alert or channel.
CREATE TABLE alert_notification_recipient_deliveries (
    alert_id       UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    channel_id     UUID NOT NULL REFERENCES alert_channels(id) ON DELETE CASCADE,
    event_key      TEXT NOT NULL,
    recipient_hash TEXT NOT NULL,
    delivered_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (alert_id, channel_id, event_key, recipient_hash)
);

CREATE INDEX idx_alert_notification_recipient_deliveries_channel_id
    ON alert_notification_recipient_deliveries(channel_id);
