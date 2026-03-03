-- Improves dashboard "recent alerts" query (tenant + newest first).
CREATE INDEX IF NOT EXISTS idx_alerts_tenant_triggered_at
ON alerts(tenant_id, triggered_at DESC);

-- Improves dashboard aggregations that skip platform-generated results.
CREATE INDEX IF NOT EXISTS idx_check_results_tenant_non_platform_created_monitor
ON check_results(tenant_id, created_at DESC, monitor_id)
WHERE result_source <> 'platform';

-- Improves monitor health latest-check lookup per monitor.
CREATE INDEX IF NOT EXISTS idx_check_results_monitor_non_platform_created_at
ON check_results(monitor_id, created_at DESC)
WHERE result_source <> 'platform';

-- Improves dashboard recent-failures outer scan.
CREATE INDEX IF NOT EXISTS idx_check_results_tenant_failure_created_monitor
ON check_results(tenant_id, created_at DESC, monitor_id)
WHERE status IN ('failure', 'error');

-- Improves dashboard recent-failures lateral "next success" lookup.
CREATE INDEX IF NOT EXISTS idx_check_results_tenant_monitor_success_created_at
ON check_results(tenant_id, monitor_id, created_at ASC)
WHERE status = 'success';
