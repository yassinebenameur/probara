CREATE TABLE alert_notification_states (
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES alert_channels(id) ON DELETE CASCADE,
    last_sent_at TIMESTAMPTZ NOT NULL,
    last_event_type TEXT NOT NULL CHECK (last_event_type IN ('created', 'resolved', 'reminder')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (alert_id, channel_id)
);

CREATE INDEX idx_alert_notification_states_alert_id ON alert_notification_states(alert_id);
CREATE INDEX idx_alert_notification_states_channel_id ON alert_notification_states(channel_id);
