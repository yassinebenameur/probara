-- Acknowledgement semantics: an acknowledged alert stops paging (its unfired
-- escalation tiers and its reminders), unacknowledge resumes it, and a
-- per-alert snooze mutes it until a time.
--
-- ack_stops_notifications defaults TRUE for every tenant, existing ones
-- included: acknowledging is the operator saying "someone is on it".
ALTER TABLE tenants
    ADD COLUMN ack_stops_notifications BOOLEAN NOT NULL DEFAULT TRUE;

-- Per-route opt-out: a route marked notify_after_ack keeps receiving tiers
-- and reminders while the alert is acknowledged (e.g. a status-room channel).
ALTER TABLE monitor_channels
    ADD COLUMN notify_after_ack BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE tenant_default_channels
    ADD COLUMN notify_after_ack BOOLEAN NOT NULL DEFAULT FALSE;

-- Who acknowledged and through which surface; when it was last
-- unacknowledged (the escalation and reminder clocks resume from it); and
-- the snooze. Snooze is independent of ack and never writes a maintenance
-- window, so SLA planned downtime is untouched.
ALTER TABLE alerts
    ADD COLUMN acknowledged_by_user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    ADD COLUMN acknowledged_via TEXT,
    ADD COLUMN unacknowledged_at TIMESTAMPTZ,
    ADD COLUMN snoozed_until TIMESTAMPTZ,
    ADD COLUMN snoozed_by_user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL;
