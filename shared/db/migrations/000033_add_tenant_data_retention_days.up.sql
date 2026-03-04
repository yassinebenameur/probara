ALTER TABLE tenants
ADD COLUMN data_retention_days INTEGER NOT NULL DEFAULT 0;

ALTER TABLE tenants
ADD CONSTRAINT tenants_data_retention_days_check
CHECK (
    data_retention_days = 0
    OR (data_retention_days BETWEEN 30 AND 3650)
);
