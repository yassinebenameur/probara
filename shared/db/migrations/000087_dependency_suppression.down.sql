-- 000087_dependency_suppression.down.sql
ALTER TABLE alerts DROP COLUMN IF EXISTS root_cause_cleared_at;
ALTER TABLE monitors DROP COLUMN IF EXISTS dependency_suppression;
ALTER TABLE tenants
    DROP COLUMN IF EXISTS dependency_suppression_grace_seconds,
    DROP COLUMN IF EXISTS dependency_suppression_enabled;
