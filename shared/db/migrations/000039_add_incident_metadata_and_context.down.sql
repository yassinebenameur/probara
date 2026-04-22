-- Roll back incident metadata and owner context.

DROP INDEX IF EXISTS idx_incidents_tenant_severity_updated_at;
DROP INDEX IF EXISTS idx_incidents_owner_user_id;

ALTER TABLE incidents
    DROP CONSTRAINT IF EXISTS incidents_severity_check;

ALTER TABLE incidents
    DROP COLUMN IF EXISTS owner_user_id,
    DROP COLUMN IF EXISTS severity;
