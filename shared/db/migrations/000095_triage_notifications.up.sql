-- Opt-in suspected-cause follow-ups (alerter dispatchSuspectedCauses).
-- Supersedes 000094's "never read by the alerter": the alerter now reads
-- ready alert_triage rows for this one advisory message and nothing else.
--
-- One-shot per (alert, channel), like acknowledged_sent_at (000089): the
-- alerter's claim sets it only while it is NULL, and it never touches
-- last_event_type/last_sent_at, so created/reminder/resolved timing is
-- unchanged.
ALTER TABLE alert_notification_states ADD COLUMN suspected_cause_sent_at TIMESTAMPTZ;

-- Workspace opt-in; effective only while enabled AND auto_triage_enabled.
-- *_enabled_at is stamped when the flag turns on (cleared when it turns off):
-- only triages completed after it are sent, so opting in mid-incident does not
-- flush every recent triage at once.
ALTER TABLE ai_settings
    ADD COLUMN triage_notifications_enabled    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN triage_notifications_enabled_at TIMESTAMPTZ;
