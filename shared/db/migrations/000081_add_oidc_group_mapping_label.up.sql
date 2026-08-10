-- Operator-facing display name for a mapping. Purely cosmetic: matching
-- always uses group_name (the raw claim value — Azure emits GUIDs there,
-- which are correct but unreadable in the settings table).
ALTER TABLE oidc_group_mappings ADD COLUMN label TEXT;
