'use client';

import { useState, useEffect } from 'react';
import {
  Monitor,
  CheckResult,
  AlertPolicy,
  HTTPMonitorConfig,
  PingMonitorConfig,
  DNSMonitorConfig,
  GRPCMonitorConfig,
  HTTPMetricsEnvelope,
  GRPCMetricsEnvelope,
  SyntheticAPIMonitorConfig,
  SyntheticBrowserMonitorConfig,
  SyntheticBrowserMetricsEnvelope,
} from '@/lib/types';
import { getMonitorResults, getAlertPolicy, getSyntheticBrowserScreenshotUrl } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import {
  calculateUptime,
  countOperationalResults,
  formatInterval,
  formatTimeAgo,
  calculateLatencyStats,
  getOperationalResults,
} from '@/lib/monitor-utils';
import TagPill from '@/components/ui/TagPill';

interface MonitorDetailPanelProps {
  monitor: Monitor | null;
}

export default function MonitorDetailPanel({ monitor }: MonitorDetailPanelProps) {
  const [checkResults, setCheckResults] = useState<CheckResult[]>([]);
  const [alertPolicies, setAlertPolicies] = useState<AlertPolicy[]>([]);
  const [loading, setLoading] = useState(false);
  const [screenshotBlobURL, setScreenshotBlobURL] = useState<string | null>(null);
  const [screenshotLoading, setScreenshotLoading] = useState(false);

  const formatExpectedStatusRules = (cfg: HTTPMonitorConfig) => {
    const parts: string[] = [];
    if (cfg.expected_status !== undefined) parts.push(String(cfg.expected_status));
    if (cfg.expected_statuses?.length) parts.push(cfg.expected_statuses.join(','));
    if (cfg.expected_status_ranges?.length) parts.push(...cfg.expected_status_ranges.map(r => `${r.min}-${r.max}`));
    if (cfg.expected_status_classes?.length) parts.push(...cfg.expected_status_classes);
    return parts.length ? parts.join(', ') : '2xx';
  };

  const getHTTPMetrics = (result?: CheckResult) => {
    const md = result?.metrics_data as unknown;
    if (!md || typeof md !== 'object') return null;
    const env = md as HTTPMetricsEnvelope;
    return env.http || null;
  };

  const getSyntheticBrowserMetrics = (result?: CheckResult) => {
    const md = result?.metrics_data as unknown;
    if (!md || typeof md !== 'object') return null;
    const env = md as SyntheticBrowserMetricsEnvelope;
    return env.synthetic_browser || null;
  };

  const getGRPCMetrics = (result?: CheckResult) => {
    const md = result?.metrics_data as unknown;
    if (!md || typeof md !== 'object') return null;
    const env = md as GRPCMetricsEnvelope;
    return env.grpc || null;
  };

  // Helper to get config safely (handles both old and new formats)
  const getHTTPConfig = (mon: Monitor): HTTPMonitorConfig | null => {
    if (mon.type !== 'http') return null;
    // New format: config object exists
    if (mon.config && typeof mon.config === 'object') {
      return mon.config as HTTPMonitorConfig;
    }
    // Old format: fields directly on monitor (for backward compatibility)
    const oldMonitor = mon as any;
    if (oldMonitor.url) {
      return {
        url: oldMonitor.url,
        method: oldMonitor.method || 'GET',
        headers: oldMonitor.headers,
        body: oldMonitor.body,
        expected_status: oldMonitor.expected_status,
        expected_body_substring: oldMonitor.expected_body_substring,
      };
    }
    return null;
  };

  const getPingConfig = (mon: Monitor): PingMonitorConfig | null => {
    if (mon.type !== 'ping') return null;
    if (mon.config && typeof mon.config === 'object') {
      return mon.config as PingMonitorConfig;
    }
    return null;
  };

  const getDNSConfig = (mon: Monitor): DNSMonitorConfig | null => {
    if (mon.type !== 'dns') return null;
    if (mon.config && typeof mon.config === 'object') {
      return mon.config as DNSMonitorConfig;
    }
    return null;
  };

  const getGRPCConfig = (mon: Monitor): GRPCMonitorConfig | null => {
    if (mon.type !== 'grpc') return null;
    if (mon.config && typeof mon.config === 'object') {
      return mon.config as GRPCMonitorConfig;
    }
    return null;
  };

  const getSyntheticAPIConfig = (mon: Monitor): SyntheticAPIMonitorConfig | null => {
    if (mon.type !== 'synthetic_api') return null;
    if (mon.config && typeof mon.config === 'object') {
      return mon.config as SyntheticAPIMonitorConfig;
    }
    return null;
  };

  const getSyntheticBrowserConfig = (mon: Monitor): SyntheticBrowserMonitorConfig | null => {
    if (mon.type !== 'synthetic_browser') return null;
    if (mon.config && typeof mon.config === 'object') {
      return mon.config as SyntheticBrowserMonitorConfig;
    }
    return null;
  };

  useEffect(() => {
    if (!monitor) {
      setCheckResults([]);
      setAlertPolicies([]);
      return;
    }

    const fetchData = async () => {
      setLoading(true);
      try {
        // Fetch check results
        const resultsResponse = await getMonitorResults(monitor.id, { limit: 100 });
        setCheckResults(resultsResponse.results || []);

        const policyIDs = monitor.alert_policy_ids?.length
          ? monitor.alert_policy_ids
          : monitor.alert_policy_id
            ? [monitor.alert_policy_id]
            : [];

        if (policyIDs.length > 0) {
          try {
            const policies = await Promise.all(policyIDs.map((id) => getAlertPolicy(id)));
            setAlertPolicies(policies);
          } catch (err) {
            console.error('Failed to fetch alert policies:', err);
            setAlertPolicies([]);
          }
        } else {
          setAlertPolicies([]);
        }
      } catch (err) {
        console.error('Failed to fetch monitor details:', err);
        setCheckResults([]);
      } finally {
        setLoading(false);
      }
    };

    fetchData();
  }, [monitor]);

  const uptime = calculateUptime(checkResults);
  const operationalResults = getOperationalResults(checkResults);
  const operationalCount = countOperationalResults(checkResults);
  const latencyStats = calculateLatencyStats(checkResults);
  const recentResults = checkResults.slice(0, 10);
  const latestOperationalResult = operationalResults[0];
  const latestHTTPMetrics = monitor?.type === 'http' ? getHTTPMetrics(latestOperationalResult) : null;
  const latestTLS = latestHTTPMetrics?.tls;
  const latestSyntheticBrowserResult = monitor?.type === 'synthetic_browser'
    ? checkResults.find((result) => {
        const metrics = getSyntheticBrowserMetrics(result);
        return Boolean(metrics?.artifacts?.screenshot_path);
      })
    : undefined;
  const latestSyntheticBrowserMetrics = getSyntheticBrowserMetrics(latestSyntheticBrowserResult);
  const latestGRPCMetrics = monitor?.type === 'grpc' ? getGRPCMetrics(latestOperationalResult) : null;
  const latestSyntheticBrowserScreenshot = latestSyntheticBrowserMetrics?.artifacts?.screenshot_path;
  const syntheticBrowserScreenshotURL = monitor && latestSyntheticBrowserScreenshot
    ? getSyntheticBrowserScreenshotUrl(monitor.id, monitor.tenant_id, latestSyntheticBrowserScreenshot)
    : null;

  useEffect(() => {
    let cancelled = false;
    let objectURL: string | null = null;

    if (!syntheticBrowserScreenshotURL) {
      setScreenshotBlobURL((prev) => {
        if (prev) URL.revokeObjectURL(prev);
        return null;
      });
      setScreenshotLoading(false);
      return;
    }

    const loadScreenshot = async () => {
      setScreenshotLoading(true);
      try {
        const headers: HeadersInit = {};
        const apiKey = getApiKey();
        if (apiKey) {
          headers.Authorization = `Bearer ${apiKey}`;
        }

        const response = await fetch(syntheticBrowserScreenshotURL, {
          method: 'GET',
          headers,
          credentials: 'include',
        });

        if (!response.ok) {
          throw new Error(`screenshot request failed: ${response.status}`);
        }

        const blob = await response.blob();
        objectURL = URL.createObjectURL(blob);
        if (!cancelled) {
          setScreenshotBlobURL((prev) => {
            if (prev) URL.revokeObjectURL(prev);
            return objectURL;
          });
        }
      } catch (err) {
        console.error('Failed to load synthetic browser screenshot:', err);
        if (!cancelled) {
          setScreenshotBlobURL((prev) => {
            if (prev) URL.revokeObjectURL(prev);
            return null;
          });
        }
      } finally {
        if (!cancelled) {
          setScreenshotLoading(false);
        }
      }
    };

    loadScreenshot();

    return () => {
      cancelled = true;
      if (objectURL) {
        URL.revokeObjectURL(objectURL);
      }
    };
  }, [syntheticBrowserScreenshotURL]);

  if (!monitor) {
    return (
      <div className="relative overflow-hidden rounded-[22px] border border-[rgba(255,255,255,0.14)] bg-[rgba(15,23,42,0.92)] p-3.5 shadow-subtle">
        <div className="flex h-64 items-center justify-center text-sm text-muted">
          Select a monitor to view details
        </div>
      </div>
    );
  }

  return (
    <div className="relative overflow-hidden rounded-[22px] border border-[rgba(255,255,255,0.14)] bg-[rgba(15,23,42,0.92)] p-3.5 shadow-subtle">
      {/* Header */}
      <div className="mb-2.5 flex flex-col gap-1">
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2 text-[0.9rem] font-medium">
            {monitor.name}
            <span className="rounded-full border border-[rgba(148,163,184,0.6)] px-2 py-0.5 text-[0.72rem]">
              {monitor.type.toUpperCase()} · {monitor.enabled ? 'Active' : 'Disabled'}
            </span>
          </div>
          <span className={monitor.enabled ? 'badge badge-success' : 'badge badge-default'}>
            {monitor.enabled ? 'Alerts enabled' : 'Alerts disabled'}
          </span>
        </div>
        <div className="text-xs text-muted">
          {monitor.tags && monitor.tags.length > 0
            ? `Tagged: ${monitor.tags.join(', ')}`
            : 'No tags'}
        </div>
      </div>

      {loading ? (
        <div className="flex h-48 items-center justify-center">
          <div className="text-sm text-muted">Loading details...</div>
        </div>
      ) : (
        <>
          {/* Snapshot Metrics */}
          <div className="mt-2.5 border-t border-dashed border-[rgba(255,255,255,0.06)] pt-2">
            <div className="mb-1.5 text-[0.78rem] uppercase tracking-wide text-muted">
              Snapshot
            </div>
            <div className="flex flex-wrap gap-2 text-xs">
              <div className="min-w-[120px] flex-1 rounded-[14px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.96)] p-2">
                <div className="mb-0.5 text-[0.72rem] text-muted">Uptime 7 days</div>
                <div className="text-[0.9rem] font-medium">{operationalCount > 0 ? `${uptime.toFixed(2)}%` : '—'}</div>
                <div className="text-[0.72rem] text-muted">
                  {operationalCount > 0 ? `${operationalCount} checks` : 'No data'}
                </div>
              </div>
              <div className="min-w-[120px] flex-1 rounded-[14px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.96)] p-2">
                <div className="mb-0.5 text-[0.72rem] text-muted">Latency</div>
                <div className="text-[0.9rem] font-medium">P95: {latencyStats.p95} ms</div>
                <div className="text-[0.72rem] text-muted">
                  Median: {latencyStats.median} ms
                </div>
              </div>
              <div className="min-w-[120px] flex-1 rounded-[14px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.96)] p-2">
                <div className="mb-0.5 text-[0.72rem] text-muted">Last run</div>
                <div className="text-[0.9rem] font-medium">
                  {latestOperationalResult
                    ? formatTimeAgo(latestOperationalResult.created_at)
                    : 'Never'}
                </div>
                <div className="text-[0.72rem] text-muted">
                  {latestOperationalResult?.http_status
                    ? `Status code: ${latestOperationalResult.http_status}`
                    : 'No status'}
                </div>
              </div>
              {latestTLS && (
                <div className="min-w-[120px] flex-1 rounded-[14px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.96)] p-2">
                  <div className="mb-0.5 text-[0.72rem] text-muted">Certificate</div>
                  <div className="text-[0.9rem] font-medium">
                    {latestTLS.days_until_expiry !== undefined
                      ? `${latestTLS.days_until_expiry}d left`
                      : '—'}
                  </div>
                  <div className="text-[0.72rem] text-muted truncate">
                    {latestTLS.not_after
                      ? `Expires: ${new Date(latestTLS.not_after).toISOString().slice(0, 10)}`
                      : 'No TLS data'}
                  </div>
                </div>
              )}
            </div>
          </div>

          {/* Configuration */}
          <div className="mt-2.5 border-t border-dashed border-[rgba(255,255,255,0.06)] pt-2">
            <div className="mb-1.5 text-[0.78rem] uppercase tracking-wide text-muted">
              Configuration
            </div>
            <ul className="flex flex-col gap-1.5 text-xs">
              {monitor.type === 'http' && (() => {
                const httpConfig = getHTTPConfig(monitor);
                if (!httpConfig) return null;
                return (
                  <>
                    <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                      <span className="text-muted">URL</span>
                      <span className="truncate text-right text-[#e5e7eb]">
                        {httpConfig.url}
                      </span>
                    </li>
                    <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                      <span className="text-muted">Method</span>
                      <span className="text-[#e5e7eb]">
                        {httpConfig.method} · expect{' '}
                        {formatExpectedStatusRules(httpConfig)}
                      </span>
                    </li>
                    {(httpConfig.max_latency_ms ||
                      (httpConfig.body_assertions && httpConfig.body_assertions.length > 0) ||
                      (httpConfig.response_header_assertions && httpConfig.response_header_assertions.length > 0) ||
                      (httpConfig.json_assertions && httpConfig.json_assertions.length > 0)) && (
                      <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                        <span className="text-muted">Checks</span>
                        <span className="text-[#e5e7eb]">
                          {httpConfig.max_latency_ms ? `latency \u2264 ${httpConfig.max_latency_ms}ms` : 'latency default'}
                          {httpConfig.body_assertions?.length ? ` Â· body: ${httpConfig.body_assertions.length}` : ''}
                          {httpConfig.response_header_assertions?.length ? ` Â· headers: ${httpConfig.response_header_assertions.length}` : ''}
                          {httpConfig.json_assertions?.length ? ` Â· json: ${httpConfig.json_assertions.length}` : ''}
                        </span>
                      </li>
                    )}
                    {(httpConfig.follow_redirects !== undefined || httpConfig.max_redirects !== undefined) && (
                      <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                        <span className="text-muted">Redirects</span>
                        <span className="text-[#e5e7eb]">
                          {httpConfig.follow_redirects === false ? 'off' : `on Â· max ${httpConfig.max_redirects ?? 10}`}
                        </span>
                      </li>
                    )}
                    {(httpConfig.tls_min_days_valid !== undefined ||
                      httpConfig.tls_skip_verify ||
                      (httpConfig.tls_server_name && httpConfig.tls_server_name.trim())) && (
                      <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                        <span className="text-muted">TLS</span>
                        <span className="text-[#e5e7eb]">
                          {httpConfig.tls_skip_verify ? 'skip verify' : 'verify'}
                          {httpConfig.tls_min_days_valid !== undefined ? ` Â· min ${httpConfig.tls_min_days_valid}d` : ''}
                          {httpConfig.tls_server_name ? ` Â· sni ${httpConfig.tls_server_name}` : ''}
                        </span>
                      </li>
                    )}
                  </>
                );
              })()}
              {monitor.type === 'ping' && (() => {
                const pingConfig = getPingConfig(monitor);
                if (!pingConfig) return null;
                return (
                  <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                    <span className="text-muted">Host</span>
                    <span className="truncate text-right text-[#e5e7eb]">
                      {pingConfig.host}
                    </span>
                  </li>
                );
              })()}
              {monitor.type === 'dns' && (() => {
                const dnsConfig = getDNSConfig(monitor);
                if (!dnsConfig) return null;
                const expected = dnsConfig.expected_answers || [];
                return (
                  <>
                    <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                      <span className="text-muted">Host</span>
                      <span className="truncate text-right text-[#e5e7eb]">
                        {dnsConfig.host}
                      </span>
                    </li>
                    <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                      <span className="text-muted">Record</span>
                      <span className="text-[#e5e7eb]">
                        {(dnsConfig.record_type || 'A').toUpperCase()}
                      </span>
                    </li>
                    {expected.length > 0 && (
                      <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                        <span className="text-muted">Expected</span>
                        <span className="truncate text-right text-[#e5e7eb]">
                          {expected.join(', ')}
                        </span>
                      </li>
                    )}
                  </>
                );
              })()}
              {monitor.type === 'grpc' && (() => {
                const grpcConfig = getGRPCConfig(monitor);
                if (!grpcConfig) return null;
                const resolvedPort = grpcConfig.port || (grpcConfig.use_tls === false ? 80 : 443);
                return (
                  <>
                    <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                      <span className="text-muted">Target</span>
                      <span className="truncate text-right text-[#e5e7eb]">
                        {grpcConfig.host}:{resolvedPort}
                      </span>
                    </li>
                    <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                      <span className="text-muted">TLS</span>
                      <span className="text-[#e5e7eb]">
                        {grpcConfig.use_tls === false ? 'disabled' : 'enabled'}
                      </span>
                    </li>
                    {grpcConfig.service && (
                      <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                        <span className="text-muted">Service</span>
                        <span className="truncate text-right text-[#e5e7eb]">
                          {grpcConfig.service}
                        </span>
                      </li>
                    )}
                    {latestGRPCMetrics?.serving_status && (
                      <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                        <span className="text-muted">Last health</span>
                        <span className="text-[#e5e7eb]">
                          {latestGRPCMetrics.serving_status}
                        </span>
                      </li>
                    )}
                  </>
                );
              })()}
              {monitor.type === 'synthetic_api' && (() => {
                const synConfig = getSyntheticAPIConfig(monitor);
                if (!synConfig) return null;
                return (
                  <>
                    {synConfig.base_url && (
                      <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                        <span className="text-muted">Base URL</span>
                        <span className="truncate text-right text-[#e5e7eb]">
                          {synConfig.base_url}
                        </span>
                      </li>
                    )}
                    <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                      <span className="text-muted">Workflow</span>
                      <span className="text-[#e5e7eb]">
                        {(synConfig.steps || []).length} steps · mode {synConfig.failure_mode || 'fail_fast'}
                      </span>
                    </li>
                  </>
                );
              })()}
              {monitor.type === 'synthetic_browser' && (() => {
                const synConfig = getSyntheticBrowserConfig(monitor);
                if (!synConfig) return null;
                return (
                  <>
                    <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                      <span className="text-muted">Start URL</span>
                      <span className="truncate text-right text-[#e5e7eb]">
                        {synConfig.start_url}
                      </span>
                    </li>
                    <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                      <span className="text-muted">Journey</span>
                      <span className="text-[#e5e7eb]">
                        {(synConfig.steps || []).length} steps · {synConfig.device || 'default device'}
                      </span>
                    </li>
                  </>
                );
              })()}
              <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                <span className="text-muted">Schedule</span>
                <span className="text-[#e5e7eb]">
                  {formatInterval(monitor.interval_seconds)} · timeout{' '}
                  {monitor.timeout_seconds}s
                </span>
              </li>
              {monitor.tags && monitor.tags.length > 0 && (
                <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                  <span className="text-muted">Tags</span>
                  <span className="flex flex-wrap gap-1">
                    {monitor.tags.map((tag) => (
                      <TagPill key={tag}>{tag}</TagPill>
                    ))}
                  </span>
                </li>
              )}
            </ul>
          </div>

          {/* Alerting */}
          <div className="mt-2.5 border-t border-dashed border-[rgba(255,255,255,0.06)] pt-2">
            <div className="mb-1.5 text-[0.78rem] uppercase tracking-wide text-muted">
              Alerting
            </div>
            <ul className="flex flex-col gap-1.5 text-xs">
              <li className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5">
                <span className="text-muted">Policies</span>
                <span className="text-[#e5e7eb]">
                  {alertPolicies.length > 0
                    ? alertPolicies.map((policy) => (
                        <div key={policy.id}>
                          {policy.name} ({policy.failure_threshold} fails in {policy.failure_window_seconds}s)
                        </div>
                      ))
                    : 'No policy configured'}
                </span>
              </li>
            </ul>
          </div>

          {monitor.type === 'synthetic_browser' && (
            <div className="mt-2.5 border-t border-dashed border-[rgba(255,255,255,0.06)] pt-2">
              <div className="mb-1.5 text-[0.78rem] uppercase tracking-wide text-muted">
                Latest failure screenshot
              </div>
              {screenshotLoading ? (
                <div className="rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-3 text-xs text-muted">
                  Loading screenshot...
                </div>
              ) : screenshotBlobURL ? (
                <div className="overflow-hidden rounded-[14px] border border-[rgba(255,255,255,0.08)] bg-[rgba(2,6,23,0.55)] p-2">
                  <img
                    src={screenshotBlobURL}
                    alt="Latest synthetic browser screenshot"
                    loading="lazy"
                    decoding="async"
                    className="h-auto max-h-[320px] w-full rounded-[10px] border border-[rgba(255,255,255,0.08)] object-contain"
                  />
                  <div className="mt-1.5 flex items-center justify-between text-[0.72rem] text-muted">
                    <span>{latestSyntheticBrowserResult ? formatTimeAgo(latestSyntheticBrowserResult.created_at) : 'Latest run'}</span>
                    <span className="uppercase tracking-wide">
                      {latestSyntheticBrowserResult?.status || 'failure'}
                    </span>
                  </div>
                  {latestSyntheticBrowserResult?.error_message && (
                    <div className="mt-1 rounded-[8px] border border-[rgba(244,63,94,0.25)] bg-[rgba(15,23,42,0.8)] px-2 py-1 text-[0.72rem] text-rose-200">
                      {latestSyntheticBrowserResult.error_message}
                    </div>
                  )}
                </div>
              ) : (
                <div className="rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-3 text-xs text-muted">
                  No screenshot available yet. A screenshot is captured on failed runs when enabled.
                </div>
              )}
            </div>
          )}

          {/* Recent Results */}
          <div className="mt-2.5 border-t border-dashed border-[rgba(255,255,255,0.06)] pt-2">
            <div className="mb-1.5 text-[0.78rem] uppercase tracking-wide text-muted">
              Recent results
            </div>
            {recentResults.length > 0 ? (
              <ul className="flex flex-col gap-1.5 text-xs">
                {recentResults.map((result, idx) => (
                  <li
                    key={result.id || idx}
                    className="flex justify-between gap-2 rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-1.5"
                  >
                    <span className="text-muted">{formatTimeAgo(result.created_at)}</span>
                    <span className="text-[#e5e7eb]">
                      {result.http_status || 'N/A'} · {result.latency_ms || 0} ms ·{' '}
                      <span
                        className={
                          result.status === 'success'
                            ? 'text-[#bbf7d0]'
                            : 'text-[#fecaca]'
                        }
                      >
                        {result.status}
                      </span>
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <div className="rounded-[10px] border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.98)] px-2 py-3 text-center text-muted">
                No check results yet
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}
