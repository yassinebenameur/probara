-- first_acknowledged_at: the alert's first acknowledgement, set once and
-- never cleared. An unacknowledge clears acknowledged_at, so SLA reports
-- measure time to acknowledge from this column instead: ack -> unack -> a
-- later re-ack does not stretch it, and ack -> unack -> resolve still counts
-- as acknowledged. Existing rows take their current acknowledgement.
-- snoozed_at / snoozed_by_user_id: when the running snooze began and who set
-- it (NULL for an API key, or once the user is deleted), for the alert's
-- display; the mute itself reads only snoozed_until.
ALTER TABLE alerts
    ADD COLUMN first_acknowledged_at TIMESTAMPTZ,
    ADD COLUMN snoozed_at TIMESTAMPTZ,
    ADD COLUMN snoozed_by_user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL;

UPDATE alerts SET first_acknowledged_at = acknowledged_at WHERE acknowledged_at IS NOT NULL;
