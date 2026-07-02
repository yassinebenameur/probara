-- 000065_widen_monitor_state_check.up.sql
-- 'degraded': some-but-not-quorum locations of a multi-location monitor are
-- down. Distinct from 'suspect' (temporal, mid-confirmation, drives the 20s
-- fast recheck); degraded is spatial and persists at the normal interval.
-- The inline CHECK from 000044 has an auto-generated name; find it via
-- pg_constraint instead of hardcoding.
DO $$
DECLARE
    cname TEXT;
BEGIN
    SELECT con.conname INTO cname
    FROM pg_constraint con
    JOIN pg_class rel ON rel.oid = con.conrelid
    WHERE rel.relname = 'monitors'
      AND con.contype = 'c'
      AND pg_get_constraintdef(con.oid) LIKE '%current_state%';
    IF cname IS NOT NULL THEN
        EXECUTE format('ALTER TABLE monitors DROP CONSTRAINT %I', cname);
    END IF;
END $$;

ALTER TABLE monitors
    ADD CONSTRAINT monitors_current_state_check
        CHECK (current_state IN ('unknown', 'up', 'suspect', 'down', 'degraded'));
