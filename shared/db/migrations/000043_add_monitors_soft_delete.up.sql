-- Add deleted_at tombstone column so monitor deletes can be near-instant
-- while child rows (check_results, rollups, alerts, etc.) are purged asynchronously.
ALTER TABLE monitors ADD COLUMN deleted_at TIMESTAMPTZ;

-- Index for the purge worker to scan tombstoned monitors in FIFO order.
CREATE INDEX idx_monitors_deleted_at ON monitors(deleted_at) WHERE deleted_at IS NOT NULL;

-- Demote the column-level UNIQUE constraints on agent_id and push_token to
-- partial unique indexes filtered on deleted_at IS NULL. This lets a new
-- monitor reuse the same agent_id / push_token immediately after the old one
-- is soft-deleted, without waiting for the background purge.
ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_agent_id_key;
ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_push_token_key;

CREATE UNIQUE INDEX idx_monitors_agent_id_active
    ON monitors(agent_id)
    WHERE agent_id IS NOT NULL AND deleted_at IS NULL;

CREATE UNIQUE INDEX idx_monitors_push_token_active
    ON monitors(push_token)
    WHERE push_token IS NOT NULL AND deleted_at IS NULL;
