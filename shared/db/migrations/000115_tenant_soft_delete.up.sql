-- Deleting a workspace (tenant) is a soft delete followed by a background
-- purge: the API stamps deleted_at and every lookup refuses the workspace from
-- then on (shared/tenancy), and the scheduler's workspace purge
-- (scheduler/internal/scheduler/purger_tenants.go) removes its data in
-- bounded batches, table by table, before it deletes the row. A single
-- DELETE FROM tenants would cascade the whole check history in one
-- transaction.
ALTER TABLE tenants
    ADD COLUMN deleted_at TIMESTAMPTZ,
    ADD COLUMN deleted_by UUID REFERENCES admin_users(id) ON DELETE SET NULL;

-- A deleted workspace's name is free for reuse at once, while its row waits
-- for the purge.
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_name_key;
CREATE UNIQUE INDEX tenants_name_live_key ON tenants (name) WHERE deleted_at IS NULL;

-- The purge queue: deleted workspaces, oldest first.
CREATE INDEX idx_tenants_deleted_at ON tenants (deleted_at) WHERE deleted_at IS NOT NULL;
