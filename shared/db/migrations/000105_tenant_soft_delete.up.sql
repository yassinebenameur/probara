-- Workspace (tenant) delete is a soft delete followed by an asynchronous
-- purge: the API stamps deleted_at and tombstones the tenant's monitors in
-- one transaction, and the scheduler purger drains the data in bounded
-- batches before removing the tenant row. A single DELETE FROM tenants would
-- cascade every check result of the tenant in one transaction.
ALTER TABLE tenants
    ADD COLUMN deleted_at TIMESTAMPTZ,
    ADD COLUMN deleted_by UUID REFERENCES admin_users(id) ON DELETE SET NULL;

-- A deleted workspace's name is free for reuse at once, while its row waits
-- for the purger.
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_name_key;
CREATE UNIQUE INDEX tenants_name_live_key ON tenants (name) WHERE deleted_at IS NULL;

-- The purger's queue: deleted tenants, oldest first.
CREATE INDEX idx_tenants_deleted_at ON tenants (deleted_at) WHERE deleted_at IS NOT NULL;
