CREATE TABLE monitor_hourly_rollups (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    monitor_id UUID NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    bucket_hour TIMESTAMPTZ NOT NULL,
    total_checks INTEGER NOT NULL DEFAULT 0,
    success_checks INTEGER NOT NULL DEFAULT 0,
    latency_success_sum_ms DOUBLE PRECISION NOT NULL DEFAULT 0,
    latency_success_count INTEGER NOT NULL DEFAULT 0,
    latest_status TEXT,
    latest_check_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (monitor_id, bucket_hour)
);

CREATE INDEX idx_monitor_hourly_rollups_tenant_hour
    ON monitor_hourly_rollups(tenant_id, bucket_hour DESC, monitor_id);

WITH state AS (
    SELECT last_created_at, last_check_result_id
    FROM rollup_job_state
    WHERE job_name = 'monitor_daily_rollups'
      AND last_created_at IS NOT NULL
),
recent_rolled_checks AS (
    SELECT cr.*
    FROM check_results cr
    CROSS JOIN state
    WHERE cr.result_source = 'monitor'
      AND cr.created_at >= NOW() - INTERVAL '48 hours'
      AND (cr.created_at, cr.id) <= (
          state.last_created_at,
          COALESCE(state.last_check_result_id, '00000000-0000-0000-0000-000000000000'::UUID)
      )
)
INSERT INTO monitor_hourly_rollups (
    tenant_id, monitor_id, bucket_hour, total_checks, success_checks,
    latency_success_sum_ms, latency_success_count, latest_status, latest_check_at,
    created_at, updated_at
)
SELECT
    tenant_id,
    monitor_id,
    DATE_TRUNC('hour', created_at) AS bucket_hour,
    COUNT(*) AS total_checks,
    COUNT(*) FILTER (WHERE status = 'success') AS success_checks,
    COALESCE(SUM(latency_ms) FILTER (WHERE status = 'success' AND latency_ms IS NOT NULL), 0)::DOUBLE PRECISION AS latency_success_sum_ms,
    COUNT(latency_ms) FILTER (WHERE status = 'success') AS latency_success_count,
    (ARRAY_AGG(status ORDER BY created_at DESC, id DESC))[1] AS latest_status,
    MAX(created_at) AS latest_check_at,
    NOW(),
    NOW()
FROM recent_rolled_checks
GROUP BY tenant_id, monitor_id, DATE_TRUNC('hour', created_at);
