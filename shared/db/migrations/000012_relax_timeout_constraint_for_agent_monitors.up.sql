-- Drop the existing check constraint
ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_check;

-- Add a new constraint that only applies to http and ping monitors
-- For agent and group monitors, timeout is not enforced
ALTER TABLE monitors ADD CONSTRAINT monitors_timeout_check 
CHECK (
    (type IN ('http', 'ping') AND timeout_seconds > 0 AND timeout_seconds < interval_seconds)
    OR (type IN ('agent', 'group'))
);

