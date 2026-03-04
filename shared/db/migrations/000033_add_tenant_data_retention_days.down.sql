ALTER TABLE tenants
DROP CONSTRAINT IF EXISTS tenants_data_retention_days_check;

ALTER TABLE tenants
DROP COLUMN IF EXISTS data_retention_days;
