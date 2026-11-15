-- Relapse reopen (flap suppression). When a workspace sets
-- alert_reopen_cooldown_minutes (1-1440; 0, the default, is off), an alert
-- the alerter resolved on its own less than that long ago is reopened when
-- the same subject fails again, instead of a new alert being opened. The
-- alerter does it (alerter/internal/alerter/relapse.go); the API only reads
-- and writes the setting.
ALTER TABLE tenants
    ADD COLUMN alert_reopen_cooldown_minutes INTEGER NOT NULL DEFAULT 0
        CHECK (alert_reopen_cooldown_minutes BETWEEN 0 AND 1440);

-- relapse_count: how many times the alert reopened. reopened_at: the last
-- reopen; escalation delays and reminders count from it
-- (alertrouting.DispatchResumedAtExpr), as after an unacknowledge.
-- auto_resolved: TRUE only when the alerter resolved the alert itself (the
-- subject recovered, or its monitor was paused or deleted). A manual resolve
-- leaves it FALSE and never reopens. Rows resolved before this migration stay
-- FALSE: which resolver closed them is unknown, so they never reopen.
-- first_ack_episode_started_at: when the episode the first acknowledgement
-- answered began (triggered_at, or the reopen it followed), set together with
-- first_acknowledged_at and never cleared. SLA time to acknowledge is the gap
-- between the two, so a relapse never stretches it across a recovery.
ALTER TABLE alerts
    ADD COLUMN relapse_count INTEGER NOT NULL DEFAULT 0 CHECK (relapse_count >= 0),
    ADD COLUMN reopened_at TIMESTAMPTZ,
    ADD COLUMN auto_resolved BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN first_ack_episode_started_at TIMESTAMPTZ;

-- No alert has reopened before this migration, so an acknowledged one
-- answered its only episode.
UPDATE alerts SET first_ack_episode_started_at = triggered_at WHERE first_acknowledged_at IS NOT NULL;

-- One row per reopen: the recovery it interrupted and when it reopened, for
-- the alert's relapse history and the "down again N minutes after recovering"
-- wording.
CREATE TABLE alert_relapses (
    alert_id       UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    relapse_number INTEGER NOT NULL CHECK (relapse_number > 0),
    recovered_at   TIMESTAMPTZ NOT NULL,
    reopened_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (alert_id, relapse_number)
);

-- A route that heard the previous recovery is told about the relapse once:
-- the alerter's 'relapsed' claim moves its row from 'resolved' to 'relapsed'
-- (sent to the channel as a 'created' page with relapse_count > 0).
ALTER TABLE alert_notification_states
    DROP CONSTRAINT IF EXISTS alert_notification_states_last_event_type_check;
ALTER TABLE alert_notification_states
    ADD CONSTRAINT alert_notification_states_last_event_type_check
        CHECK (last_event_type IN ('created', 'resolved', 'reminder', 'relapsed'));
