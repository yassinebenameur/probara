-- 000066_add_alert_failing_locations.down.sql
ALTER TABLE alerts DROP COLUMN IF EXISTS failing_locations;
