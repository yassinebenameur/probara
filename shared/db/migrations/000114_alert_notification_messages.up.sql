-- Chat messages the alerter can edit after posting them (two-way chat
-- channels, plugins advertising plugin.CapabilityMessageUpdate): one row per
-- (alert, channel), written by the plugin through
-- shared/notifications/messagestore when it posts the alert's card.
--
-- provider_ref is the plugin's own handle on the message (Slack: channel and
-- ts). event is the alert event the card was first rendered from, so a later
-- rewrite keeps the page's details. episode is the alert's relapse_count when
-- the card was posted: a reopened alert posts a new card (the row is
-- replaced), and the old episode's card is never rewritten.
--
-- rendered_state is the alert state the card shows
-- (messagestore.CardStateSQL: the status, plus the snooze end while
-- snoozed). The alerter's `updated` claim rewrites the card whenever it
-- differs from the alert's current state, under the alert row lock, so cards
-- follow acknowledgements, unacknowledgements, snoozes and resolves from any
-- surface. Rows go with their alert or channel.
CREATE TABLE alert_notification_messages (
    alert_id       UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    channel_id     UUID NOT NULL REFERENCES alert_channels(id) ON DELETE CASCADE,
    episode        INT NOT NULL DEFAULT 0 CHECK (episode >= 0),
    provider_ref   JSONB NOT NULL,
    event          JSONB,
    rendered_state TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (alert_id, channel_id)
);

-- Channel deletes cascade here.
CREATE INDEX idx_alert_notification_messages_channel
    ON alert_notification_messages(channel_id);
-- The reconcile scans only cards that can still change: a card showing a
-- resolve is final for its episode (a reopen posts a new one).
CREATE INDEX idx_alert_notification_messages_open
    ON alert_notification_messages(alert_id)
    WHERE rendered_state <> 'resolved';
