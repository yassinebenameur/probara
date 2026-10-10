-- Paging channels (PagerDuty, Opsgenie) are told when an operator acknowledges
-- an alert they were paged for, so their own escalation stops. The ack is a
-- one-shot per (alert, channel) that must not disturb the created/reminder/
-- resolved state machine in last_event_type, so it gets its own column; the
-- alerter's claim sets it only while it is NULL.
ALTER TABLE alert_notification_states ADD COLUMN acknowledged_sent_at TIMESTAMPTZ;
