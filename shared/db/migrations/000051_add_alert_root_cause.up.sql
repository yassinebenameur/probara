ALTER TABLE alerts
    ADD COLUMN root_cause_monitor_id UUID REFERENCES monitors(id) ON DELETE SET NULL,
    ADD COLUMN root_cause_down_since TIMESTAMPTZ;

CREATE INDEX idx_alerts_root_cause_monitor ON alerts(root_cause_monitor_id)
    WHERE root_cause_monitor_id IS NOT NULL;
