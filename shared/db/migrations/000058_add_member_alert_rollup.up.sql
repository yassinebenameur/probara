-- Per-group alert roll-up. Controls what happens when monitors inside a group
-- go down:
--   'per_monitor' — each member alerts on its own; the group does not emit its
--                   own derived-down alert. For visual/category groups (e.g.
--                   "all ASRs"), where members are independent peers.
--   'group'       — members are suppressed and the group's single derived-down
--                   alert speaks for them. For composite services whose members
--                   compose one thing.
-- Only meaningful for type='group' monitors; ignored elsewhere.
--
-- New groups default to 'per_monitor' (a group should change how things look,
-- never silently swallow a member alert). Existing groups are backfilled to
-- 'group' to preserve the pre-migration behavior, where every group member was
-- unconditionally suppressed in favor of the group alert.
ALTER TABLE monitors
    ADD COLUMN member_alert_rollup TEXT NOT NULL DEFAULT 'per_monitor'
        CHECK (member_alert_rollup IN ('per_monitor', 'group'));

UPDATE monitors
SET member_alert_rollup = 'group'
WHERE type = 'group' AND deleted_at IS NULL;
