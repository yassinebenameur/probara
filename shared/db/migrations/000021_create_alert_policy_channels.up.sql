CREATE TABLE alert_policy_channels (
    alert_policy_id UUID NOT NULL REFERENCES alert_policies(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES alert_channels(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (alert_policy_id, channel_id)
);

CREATE INDEX idx_alert_policy_channels_policy_id ON alert_policy_channels(alert_policy_id);
CREATE INDEX idx_alert_policy_channels_channel_id ON alert_policy_channels(channel_id);
