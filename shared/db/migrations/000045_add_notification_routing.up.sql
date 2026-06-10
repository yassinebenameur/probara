CREATE TABLE tenant_default_channels (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES alert_channels(id) ON DELETE CASCADE,
    delay_seconds INTEGER NOT NULL DEFAULT 0 CHECK (delay_seconds >= 0),
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, channel_id)
);

CREATE TABLE monitor_channels (
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES alert_channels(id) ON DELETE CASCADE,
    delay_seconds INTEGER NOT NULL DEFAULT 0 CHECK (delay_seconds >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, channel_id)
);

ALTER TABLE monitors
    ADD COLUMN notification_mode TEXT NOT NULL DEFAULT 'default'
        CHECK (notification_mode IN ('default', 'custom'));

ALTER TABLE tenants
    ADD COLUMN alert_reminder_seconds INTEGER NOT NULL DEFAULT 3600 CHECK (alert_reminder_seconds >= 0),
    ADD COLUMN auto_create_incident BOOLEAN NOT NULL DEFAULT FALSE;

-- New lifecycle alerts carry no policy.
ALTER TABLE alerts ALTER COLUMN alert_policy_id DROP NOT NULL;

-- One open alert per monitor: resolve all but the newest open alert first,
-- then enforce with a partial unique index.
WITH ranked AS (
    SELECT id, ROW_NUMBER() OVER (PARTITION BY monitor_id ORDER BY triggered_at DESC) AS rn
    FROM alerts
    WHERE status IN ('active', 'acknowledged')
)
UPDATE alerts a
SET status = 'resolved', resolved_at = NOW(), updated_at = NOW()
FROM ranked r
WHERE a.id = r.id AND r.rn > 1;

CREATE UNIQUE INDEX idx_alerts_one_open_per_monitor
    ON alerts(monitor_id)
    WHERE status IN ('active', 'acknowledged');

-- Backfill (spec §8.2):
-- workspace default = most common policy channel set per tenant; matching
-- monitors stay 'default', others become 'custom' with their policy channels.
DO $$
DECLARE
    t RECORD;
    default_set UUID[];
BEGIN
    FOR t IN SELECT DISTINCT m.tenant_id FROM monitors m WHERE m.alert_policy_id IS NOT NULL LOOP
        -- channel set of the most common policy (ties: most recently updated policy)
        SELECT cs.channel_set INTO default_set
        FROM (
            SELECT ap.id AS policy_id, ap.updated_at,
                   (SELECT COALESCE(array_agg(apc.channel_id ORDER BY apc.channel_id), '{}')
                    FROM alert_policy_channels apc WHERE apc.alert_policy_id = ap.id) AS channel_set,
                   (SELECT COUNT(*) FROM monitors m2
                    WHERE m2.alert_policy_id = ap.id AND m2.tenant_id = t.tenant_id) AS bound
            FROM alert_policies ap
            WHERE ap.tenant_id = t.tenant_id
        ) cs
        ORDER BY cs.bound DESC, cs.updated_at DESC
        LIMIT 1;

        IF default_set IS NOT NULL THEN
            INSERT INTO tenant_default_channels (tenant_id, channel_id, position)
            SELECT t.tenant_id, cid, ord - 1
            FROM unnest(default_set) WITH ORDINALITY AS u(cid, ord)
            ON CONFLICT DO NOTHING;
        END IF;

        -- monitors whose policy channel set differs from the default -> custom
        UPDATE monitors m
        SET notification_mode = 'custom'
        WHERE m.tenant_id = t.tenant_id
          AND m.alert_policy_id IS NOT NULL
          AND (SELECT COALESCE(array_agg(apc.channel_id ORDER BY apc.channel_id), '{}')
               FROM alert_policy_channels apc
               WHERE apc.alert_policy_id = m.alert_policy_id) IS DISTINCT FROM default_set;

        INSERT INTO monitor_channels (monitor_id, channel_id)
        SELECT m.id, apc.channel_id
        FROM monitors m
        JOIN alert_policy_channels apc ON apc.alert_policy_id = m.alert_policy_id
        WHERE m.tenant_id = t.tenant_id AND m.notification_mode = 'custom'
        ON CONFLICT DO NOTHING;

        UPDATE tenants te
        SET auto_create_incident = COALESCE(
            (SELECT bool_or(ap.create_incident_on_fire) FROM alert_policies ap
             WHERE ap.tenant_id = t.tenant_id), FALSE)
        WHERE te.id = t.tenant_id;
    END LOOP;
END $$;
