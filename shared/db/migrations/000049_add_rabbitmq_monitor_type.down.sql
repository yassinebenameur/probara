-- Remove rabbitmq monitor type from the timeout validation constraint.

ALTER TABLE monitors DROP CONSTRAINT IF EXISTS monitors_timeout_check;

ALTER TABLE monitors ADD CONSTRAINT monitors_timeout_check CHECK (
    (type = ANY (ARRAY['http'::text, 'ping'::text, 'sip'::text, 'dns'::text, 'grpc'::text, 'synthetic_api'::text, 'synthetic_browser'::text, 'redis'::text, 'postgres'::text, 'mongodb'::text])) AND timeout_seconds > 0 AND timeout_seconds < interval_seconds
    OR (type = ANY (ARRAY['agent'::text, 'group'::text, 'push'::text]))
);
