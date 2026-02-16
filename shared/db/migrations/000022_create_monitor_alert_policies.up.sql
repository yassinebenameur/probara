CREATE TABLE monitor_alert_policies (
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    alert_policy_id UUID NOT NULL REFERENCES alert_policies(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, alert_policy_id)
);

CREATE INDEX idx_monitor_alert_policies_monitor_id ON monitor_alert_policies(monitor_id);
CREATE INDEX idx_monitor_alert_policies_policy_id ON monitor_alert_policies(alert_policy_id);

INSERT INTO monitor_alert_policies (monitor_id, alert_policy_id, created_at)
SELECT id, alert_policy_id, NOW()
FROM monitors
WHERE alert_policy_id IS NOT NULL
ON CONFLICT DO NOTHING;
