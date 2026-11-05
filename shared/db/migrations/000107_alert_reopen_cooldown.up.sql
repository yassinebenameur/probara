-- Flap suppression: an availability alert the alerter auto-resolved less
-- than alert_reopen_cooldown_seconds ago is reopened when its monitor goes
-- down again, instead of a new alert being opened. 0 (the default) turns it
-- off; the API bounds it to 0..86400.
ALTER TABLE tenants
    ADD COLUMN alert_reopen_cooldown_seconds INTEGER NOT NULL DEFAULT 0
        CHECK (alert_reopen_cooldown_seconds BETWEEN 0 AND 86400);

-- relapse_count counts reopens (each is a new paging episode) and
-- reopened_at stamps the last one; escalation tiers count from it.
-- resolved_by_user_id / resolved_via record a manual resolve (ui, api, mcp):
-- a manually resolved alert never reopens, and the alerter's own resolves
-- leave both NULL.
ALTER TABLE alerts
    ADD COLUMN relapse_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN reopened_at TIMESTAMPTZ,
    ADD COLUMN resolved_by_user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    ADD COLUMN resolved_via TEXT;

-- A route that heard the previous episode's resolve claims the reopened
-- page as 'reopened' (it is delivered as a 'created' with relapse_count > 0).
ALTER TABLE alert_notification_states
    DROP CONSTRAINT IF EXISTS alert_notification_states_last_event_type_check;
ALTER TABLE alert_notification_states
    ADD CONSTRAINT alert_notification_states_last_event_type_check
        CHECK (last_event_type IN ('created', 'resolved', 'reminder', 'reopened'));
