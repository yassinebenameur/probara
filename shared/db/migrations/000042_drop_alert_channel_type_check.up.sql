-- Drop the CHECK constraint restricting alert_channels.type to ('teams', 'email').
-- The plugin registry is now the source of truth for valid channel types, so
-- the database layer should not gate which integrations can be created.

ALTER TABLE alert_channels DROP CONSTRAINT IF EXISTS alert_channels_type_check;
