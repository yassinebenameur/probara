-- Restore the original CHECK constraint. This rollback will fail if any
-- existing alert_channels rows use a type other than 'teams' or 'email' —
-- delete or rewrite those rows first.

ALTER TABLE alert_channels ADD CONSTRAINT alert_channels_type_check CHECK (type IN ('teams', 'email'));
