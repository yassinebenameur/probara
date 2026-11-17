-- Workspaces still waiting for the purge are finished here (the cascade):
-- the schema being restored has no notion of a deleted workspace, so they
-- would come back to life, and the restored UNIQUE (name) could collide with
-- a live workspace that reused a deleted name.
DELETE FROM tenants WHERE deleted_at IS NOT NULL;

DROP INDEX IF EXISTS idx_tenants_deleted_at;
DROP INDEX IF EXISTS tenants_name_live_key;
ALTER TABLE tenants ADD CONSTRAINT tenants_name_key UNIQUE (name);

ALTER TABLE tenants
    DROP COLUMN IF EXISTS deleted_by,
    DROP COLUMN IF EXISTS deleted_at;
