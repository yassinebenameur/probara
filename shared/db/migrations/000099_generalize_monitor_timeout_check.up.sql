-- Passive types (shared/models.PassiveMonitorTypes: agent, group, push) carry
-- no timeout; every other type is an active check and needs
-- 0 < timeout_seconds < interval_seconds. Until now the constraint listed the
-- active types by name, so every new active type needed a migration rewriting
-- it. Naming only the passive side lets a new active type through with no
-- migration, with the same outcome for every existing type. A new passive
-- type must rewrite this constraint and PassiveMonitorTypes together
-- (TestTimeoutCheckPassiveTypesMatchGo pins the pair).

ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_timeout_check;

ALTER TABLE monitors ADD CONSTRAINT monitors_timeout_check CHECK (
    type IN ('agent', 'group', 'push')
    OR (timeout_seconds > 0 AND timeout_seconds < interval_seconds)
);
