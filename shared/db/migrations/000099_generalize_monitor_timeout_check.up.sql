-- Passive types (shared/models/passive.go PassiveMonitorTypes: agent, group,
-- push) carry no timeout; every other type is an active check and needs
-- 0 < timeout_seconds < interval_seconds. Until now the constraint listed the
-- active types by name, so each new type needed a migration rewriting it, and
-- parallel type migrations clobbered each other. Listing only the passive
-- side lets a new active type through with no migration. Same outcome for
-- every existing type; a new passive type must update this constraint and
-- PassiveMonitorTypes together.

ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_timeout_check;

ALTER TABLE monitors ADD CONSTRAINT monitors_timeout_check CHECK (
    type IN ('agent', 'group', 'push')
    OR (timeout_seconds > 0 AND timeout_seconds < interval_seconds)
);
