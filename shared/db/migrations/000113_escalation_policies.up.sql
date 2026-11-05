-- Escalation policies (people layer). A policy is a third notification mode
-- that wraps the existing routing instead of replacing it: its channel
-- targets are emitted by shared/alertrouting's TargetsSQL as ordinary
-- (channel, delay) routes, so they keep the alert_notification_states claim,
-- reminders and ack gating; its person targets (a user, whoever a schedule
-- has on call, the workspace admins) are paged by the alerter's escalation
-- loop through step claims (alert_escalation_states) and a per-contact
-- ledger (alert_escalation_pages).
CREATE TABLE escalation_policies (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    -- Extra walks of the person steps after the last step (channel targets
    -- follow the workspace reminders instead).
    repeat_count INT NOT NULL DEFAULT 0 CHECK (repeat_count BETWEEN 0 AND 10),
    -- Pause after the last step before the next walk starts.
    repeat_after_seconds INT NOT NULL DEFAULT 1800 CHECK (repeat_after_seconds BETWEEN 60 AND 86400),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);

-- Steps fire in position order; delay_seconds counts from when the alert
-- became eligible to page (the same clock as channel escalation tiers).
CREATE TABLE escalation_policy_steps (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    policy_id UUID NOT NULL REFERENCES escalation_policies(id) ON DELETE CASCADE,
    position INT NOT NULL CHECK (position >= 0),
    delay_seconds INT NOT NULL CHECK (delay_seconds BETWEEN 0 AND 604800),
    UNIQUE (policy_id, position)
);

CREATE TABLE escalation_policy_targets (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    step_id UUID NOT NULL REFERENCES escalation_policy_steps(id) ON DELETE CASCADE,
    position INT NOT NULL DEFAULT 0,
    kind TEXT NOT NULL CHECK (kind IN ('user', 'schedule', 'channel', 'all_admins')),
    user_id UUID REFERENCES admin_users(id) ON DELETE CASCADE,
    schedule_id UUID REFERENCES oncall_schedules(id) ON DELETE CASCADE,
    channel_id UUID REFERENCES alert_channels(id) ON DELETE CASCADE,
    -- Channel targets only: keep paging this route while acknowledged.
    notify_after_ack BOOLEAN NOT NULL DEFAULT FALSE,
    CHECK ((kind = 'user') = (user_id IS NOT NULL)),
    CHECK ((kind = 'schedule') = (schedule_id IS NOT NULL)),
    CHECK ((kind = 'channel') = (channel_id IS NOT NULL)),
    CHECK (kind = 'channel' OR NOT notify_after_ack)
);

CREATE INDEX idx_escalation_policy_targets_step ON escalation_policy_targets(step_id);
CREATE INDEX idx_escalation_policy_targets_user ON escalation_policy_targets(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_escalation_policy_targets_schedule ON escalation_policy_targets(schedule_id) WHERE schedule_id IS NOT NULL;
CREATE INDEX idx_escalation_policy_targets_channel ON escalation_policy_targets(channel_id) WHERE channel_id IS NOT NULL;

-- notification_mode 'policy' routes a monitor through its policy. The
-- foreign keys are NO ACTION (checked at statement end), so a workspace
-- purge that cascades to both sides succeeds, while deleting a policy that
-- is still assigned fails.
ALTER TABLE monitors
    ADD COLUMN escalation_policy_id UUID REFERENCES escalation_policies(id);
ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_notification_mode_check;
ALTER TABLE monitors
    ADD CONSTRAINT monitors_notification_mode_check
        CHECK (notification_mode IN ('default', 'custom', 'policy')),
    ADD CONSTRAINT monitors_escalation_policy_mode_check
        CHECK (notification_mode <> 'policy' OR escalation_policy_id IS NOT NULL);
CREATE INDEX idx_monitors_escalation_policy ON monitors(escalation_policy_id) WHERE escalation_policy_id IS NOT NULL;

-- Every write path that moves a monitor off 'policy' (form, bulk alerting,
-- discovery) drops the assignment with it, so a stale reference never
-- blocks deleting the policy.
CREATE OR REPLACE FUNCTION monitors_clear_escalation_policy() RETURNS trigger AS $$
BEGIN
    IF NEW.notification_mode <> 'policy' THEN
        NEW.escalation_policy_id := NULL;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_monitors_clear_escalation_policy
    BEFORE INSERT OR UPDATE OF notification_mode, escalation_policy_id ON monitors
    FOR EACH ROW EXECUTE FUNCTION monitors_clear_escalation_policy();

-- When set, default-mode monitors (and monitor-less alerts) route through
-- this policy instead of tenant_default_channels.
ALTER TABLE tenants
    ADD COLUMN default_escalation_policy_id UUID REFERENCES escalation_policies(id);

-- Step claims: one row per fired person step, per paging episode
-- (alerts.relapse_count) and repeat cycle. INSERT ... ON CONFLICT DO
-- NOTHING is the claim, so alerter replicas never page a step twice.
CREATE TABLE alert_escalation_states (
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    relapse_count INT NOT NULL DEFAULT 0,
    cycle INT NOT NULL CHECK (cycle >= 0),
    step_position INT NOT NULL CHECK (step_position >= 0),
    policy_id UUID,
    fired_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (alert_id, relapse_count, cycle, step_position)
);

-- Who was sent what: one row per (alert, contact, ledger key). A step's
-- page is keyed escalation:<pos>:<cycle>[:<episode>], so a step retried
-- after a transient failure skips the contacts it already reached; the
-- acknowledged and resolved notices go to every contact paged in the
-- alert's current episode and are claimed here too.
CREATE TABLE alert_escalation_pages (
    alert_id UUID NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
    contact_id UUID NOT NULL REFERENCES user_contacts(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES admin_users(id) ON DELETE CASCADE,
    relapse_count INT NOT NULL DEFAULT 0,
    event TEXT NOT NULL CHECK (event IN ('page', 'acknowledged', 'resolved')),
    delivery_key TEXT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (alert_id, contact_id, delivery_key)
);

CREATE INDEX idx_alert_escalation_pages_recent ON alert_escalation_pages(sent_at);

-- Person sends in the delivery history: an email page has no channel (it
-- goes through the platform mailer); an SMS page records the workspace's
-- SMS channel and the person. recipient is the masked address. contact_kind
-- is the contact's kind at send time, so a channel-less row reads as what it
-- was (an SMS page that failed for want of an SMS channel has no channel
-- either) after the contact itself is gone.
ALTER TABLE alert_notification_deliveries
    ALTER COLUMN channel_id DROP NOT NULL,
    ADD COLUMN user_id UUID REFERENCES admin_users(id) ON DELETE SET NULL,
    ADD COLUMN contact_id UUID REFERENCES user_contacts(id) ON DELETE SET NULL,
    ADD COLUMN contact_kind TEXT CHECK (contact_kind IN ('email', 'sms')),
    ADD COLUMN recipient TEXT,
    ADD CONSTRAINT alert_notification_deliveries_target_check
        CHECK (channel_id IS NOT NULL OR recipient IS NOT NULL);
