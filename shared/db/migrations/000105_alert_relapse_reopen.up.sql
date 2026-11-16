-- Relapse reopen (flap suppression). When a workspace sets
-- alert_reopen_cooldown_minutes (1-1440; 0, the default, is off), an alert
-- the alerter resolved because its condition cleared less than that long ago
-- is reopened when the same subject fails again, instead of a new alert being
-- opened. The alerter does it (alerter/internal/alerter/relapse.go); the API
-- only reads and writes the setting.
--
-- Locking: golang-migrate runs this file as one statement batch, i.e. one
-- implicit transaction, so every lock taken here is held to the end. Each
-- step is therefore kept metadata-only: columns with constant defaults, CHECKs
-- added NOT VALID (enforced on every write from now on, but no scan of the
-- existing rows, which satisfy them anyway: the new columns hold their
-- default and the notification-state CHECK only widens), and no backfill. A
-- VALIDATE CONSTRAINT here would scan under the ACCESS EXCLUSIVE locks the
-- ALTERs already hold, so it is left out; a later migration can validate
-- under SHARE UPDATE EXCLUSIVE if it is ever wanted.
ALTER TABLE tenants
    ADD COLUMN alert_reopen_cooldown_minutes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tenants
    ADD CONSTRAINT tenants_alert_reopen_cooldown_minutes_check
        CHECK (alert_reopen_cooldown_minutes BETWEEN 0 AND 1440) NOT VALID;

-- relapse_count: how many times the alert reopened. reopened_at: the last
-- reopen; escalation delays and reminders count from it
-- (alertrouting.DispatchResumedAtExpr), as after an unacknowledge.
-- auto_resolved: TRUE only when the alerter resolved the alert because its
-- condition cleared (the monitor came back up, the metric or latency
-- returned to normal, the certificate was renewed, the mesh edge recovered).
-- A manual resolve, or the alerter closing an alert because its monitor was
-- paused, deleted, or its rule removed, leaves it FALSE and never reopens.
-- Rows resolved before this migration stay FALSE: which resolver closed them
-- is unknown, so they never reopen.
-- first_ack_episode_started_at: when the episode the first acknowledgement
-- answered began (the reopen it followed), set together with
-- first_acknowledged_at and never cleared. NULL means the first episode
-- (triggered_at), which is every acknowledgement made before this
-- migration, so no backfill is needed. SLA time to acknowledge is the gap
-- between the two, so a relapse never stretches it across a recovery.
ALTER TABLE alerts
    ADD COLUMN relapse_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN reopened_at TIMESTAMPTZ,
    ADD COLUMN auto_resolved BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN first_ack_episode_started_at TIMESTAMPTZ;
ALTER TABLE alerts
    ADD CONSTRAINT alerts_relapse_count_check CHECK (relapse_count >= 0) NOT VALID;

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

-- paged_relapse: the alert episode (relapse_count) of the last page claimed
-- for this route. A route last paged in an earlier episode is due one
-- relapse notice. The 'relapsed' state is that notice's claim (sent to the
-- channel as a 'created' page).
-- delivered_relapse: the episode of the last page actually delivered to the
-- route (written after the send, by the alerter's synchronous path or the
-- delivery worker; NULL until one is, read as paged_relapse). Paging
-- providers key their incident per episode, so the resolve, the
-- acknowledgement and a relapse notice's close of the earlier incident all
-- target the incident this records as open, never one only claimed.
ALTER TABLE alert_notification_states
    ADD COLUMN paged_relapse INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN delivered_relapse INTEGER;
ALTER TABLE alert_notification_states
    DROP CONSTRAINT IF EXISTS alert_notification_states_last_event_type_check;
ALTER TABLE alert_notification_states
    ADD CONSTRAINT alert_notification_states_last_event_type_check
        CHECK (last_event_type IN ('created', 'resolved', 'reminder', 'relapsed')) NOT VALID;
