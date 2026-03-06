CREATE INDEX IF NOT EXISTS idx_check_results_rollup_monitor_cursor
ON check_results(created_at ASC, id ASC)
INCLUDE (tenant_id, monitor_id, status, latency_ms)
WHERE result_source = 'monitor';
