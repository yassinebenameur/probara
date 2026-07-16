-- Rolling this back is destructive in one specific way: while the feature was
-- live, callers may have created a new monitor reusing the agent_id / push_token
-- of an older tombstoned row. Restoring the global UNIQUE constraints would
-- fail with a duplicate-key error in that case. So we first purge tombstoned
-- rows (their child cascades drain via the existing ON DELETE CASCADE FKs);
-- then the global constraints can be safely restored.
--
-- Before the DELETE we have to demote any auto-created incidents that still
-- point at these tombstones: incidents_auto_fields_check (migration 000038)
-- forbids auto_monitor_id = NULL while is_auto_created is true, but the FK
-- on auto_monitor_id is ON DELETE SET NULL. Without this demotion the DELETE
-- trips the CHECK and the rollback fails. The runtime purger does the same
-- thing in scheduler/internal/scheduler/purger.go.
UPDATE incidents
SET is_auto_created = FALSE,
    auto_monitor_id = NULL,
    auto_alert_policy_id = NULL,
    updated_at = NOW()
WHERE auto_monitor_id IN (SELECT id FROM monitors WHERE deleted_at IS NOT NULL);

DELETE FROM monitors WHERE deleted_at IS NOT NULL;

DROP INDEX IF EXISTS idx_monitors_push_token_active;
DROP INDEX IF EXISTS idx_monitors_agent_id_active;

ALTER TABLE monitors ADD CONSTRAINT monitors_push_token_key UNIQUE (push_token);
ALTER TABLE monitors ADD CONSTRAINT monitors_agent_id_key UNIQUE (agent_id);

DROP INDEX IF EXISTS idx_monitors_deleted_at;
ALTER TABLE monitors DROP COLUMN IF EXISTS deleted_at;
