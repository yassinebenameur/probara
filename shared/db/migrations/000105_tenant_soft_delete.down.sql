-- Soft-deleted tenants are already waiting for the purger; finish them here
-- (the cascade) so the restored UNIQUE (name) cannot collide with a live
-- tenant that reused a deleted name.
DELETE FROM tenants WHERE deleted_at IS NOT NULL;

DROP INDEX IF EXISTS idx_tenants_deleted_at;
DROP INDEX IF EXISTS tenants_name_live_key;
ALTER TABLE tenants ADD CONSTRAINT tenants_name_key UNIQUE (name);

ALTER TABLE tenants
    DROP COLUMN IF EXISTS deleted_by,
    DROP COLUMN IF EXISTS deleted_at;
