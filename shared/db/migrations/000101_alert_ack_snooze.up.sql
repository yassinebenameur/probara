-- Acknowledge stops an alert's notifications (only the resolve notice still
-- goes out), unacknowledge resumes them, and a per-alert snooze mutes them
-- until a time without acknowledging. The mute rule itself lives in
-- shared/alertrouting (MutedPredicate, MuteHolds); these columns feed it.
--
-- acknowledged_by_user_id: who acknowledged (NULL for an API key, or once
-- the user is deleted). Cleared with acknowledged_at by an unacknowledge.
-- unacknowledged_at: the last unacknowledge. Unfired escalation tiers and
-- reminders count from it (and from a snooze end), so lifting a mute never
-- releases a burst of overdue notifications.
-- snoozed_until: the snooze end. An early unsnooze sets it to the moment it
-- ended rather than clearing it, for the same clock. A snooze never writes a
-- maintenance window, so SLA planned downtime is untouched.
ALTER TABLE alerts
    ADD COLUMN acknowledged_by_user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    ADD COLUMN unacknowledged_at TIMESTAMPTZ,
    ADD COLUMN snoozed_until TIMESTAMPTZ;
