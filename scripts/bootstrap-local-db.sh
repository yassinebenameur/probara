#!/usr/bin/env bash

set -euo pipefail

service_name="${1:-postgres}"
bootstrap_user="${BOOTSTRAP_DB_USER:-probara}"

docker compose exec -T "${service_name}" psql -U "${bootstrap_user}" -d postgres -v ON_ERROR_STOP=1 <<'SQL'
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'probara') THEN
        CREATE ROLE probara LOGIN PASSWORD 'probara';
    ELSE
        ALTER ROLE probara WITH LOGIN PASSWORD 'probara';
    END IF;
END
$$;

SELECT 'CREATE DATABASE probara OWNER probara'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'probara') \gexec

GRANT ALL PRIVILEGES ON DATABASE probara TO probara;
SQL
