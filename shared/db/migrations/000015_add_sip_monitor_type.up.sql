-- Add SIP monitor type to the timeout check constraint
-- SIP monitors are active check types that require timeout validation (like http and ping)

-- Drop the existing constraint
ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_timeout_check;

-- Recreate with SIP included in active check types
ALTER TABLE monitors ADD CONSTRAINT monitors_timeout_check CHECK (
    (type = ANY (ARRAY['http'::text, 'ping'::text, 'sip'::text])) AND timeout_seconds > 0 AND timeout_seconds < interval_seconds
    OR (type = ANY (ARRAY['agent'::text, 'group'::text, 'push'::text]))
);
