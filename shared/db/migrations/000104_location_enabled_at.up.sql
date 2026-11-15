-- When a location was last enabled. The offline rule (S-F6) waits one
-- threshold from it, so a location re-enabled before its worker is back does
-- not page on the next tick. Existing locations count as enabled since their
-- creation: their heartbeat history already says whether they are offline.
ALTER TABLE locations
    ADD COLUMN enabled_at TIMESTAMPTZ;

UPDATE locations SET enabled_at = created_at;

ALTER TABLE locations
    ALTER COLUMN enabled_at SET DEFAULT NOW(),
    ALTER COLUMN enabled_at SET NOT NULL;
