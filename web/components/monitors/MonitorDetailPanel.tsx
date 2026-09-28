'use client';

import { useState, useEffect } from 'react';
import Image from 'next/image';
import {
  Monitor,
  CheckResult,
  HTTPMonitorConfig,
  PingMonitorConfig,
  DNSMonitorConfig,
  GRPCMonitorConfig,
  HTTPMetricsEnvelope,
  GRPCMetricsEnvelope,
  SyntheticAPIMonitorConfig,
  SyntheticBrowserMonitorConfig,
  SyntheticBrowserMetricsEnvelope,
  RedisMonitorConfig,
  PostgresMonitorConfig,
  MongoDBMonitorConfig,
  RabbitMQMonitorConfig,
  DBMetrics,
  MongoDBMetrics,
  DBMetricsEnvelope,
} from '@/lib/types';
import { getMonitorResults, getSyntheticBrowserScreenshotUrl } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import {
  calculateUptime,
  countOperationalResults,
  formatInterval,
  formatTimeAgo,
  calculateLatencyStats,
  getOperationalResults,
} from '@/lib/monitor-utils';
import PrometheusDetails from './PrometheusDetails';
import Pill from '@/components/ui/Pill';
import CopyableTarget from '@/components/ui/CopyableTarget';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';

const SECTION_TITLE = 'mb-2 text-[13px] font-medium text-slate-300';

interface MonitorDetailPanelProps {
  monitor: Monitor | null;
}

export default function MonitorDetailPanel({ monitor }: MonitorDetailPanelProps) {
  const [checkResults, setCheckResults] = useState<CheckResult[]>([]);
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

  const isDatabaseType = (t: string): t is 'redis' | 'postgres' | 'mongodb' | 'rabbitmq' =>
    t === 'redis' || t === 'postgres' || t === 'mongodb' || t === 'rabbitmq';

  // Shared shape for the database/broker types. Secret fields arrive masked
  // ("***") from the API and are never displayed.
  const getDatabaseConfig = (
    mon: Monitor
  ): (RedisMonitorConfig & PostgresMonitorConfig & MongoDBMonitorConfig & RabbitMQMonitorConfig) | null => {
    if (!isDatabaseType(mon.type)) return null;
    if (mon.config && typeof mon.config === 'object') {
      return mon.config as RedisMonitorConfig & PostgresMonitorConfig & MongoDBMonitorConfig & RabbitMQMonitorConfig;
    }
    return null;
  };

  const getDBMetrics = (mon: Monitor | null, result?: CheckResult): DBMetrics | null => {
    if (!mon || !isDatabaseType(mon.type)) return null;
    const md = result?.metrics_data as unknown;
    if (!md || typeof md !== 'object') return null;
    return (md as DBMetricsEnvelope)[mon.type] || null;
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
      return;
    }

    const fetchData = async () => {
      setLoading(true);
      try {
        const resultsResponse = await getMonitorResults(monitor.id, { limit: 100 });
        setCheckResults(resultsResponse.results || []);
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
  const latestDBMetrics = getDBMetrics(monitor, latestOperationalResult);
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
      <div className="glass flex h-64 items-center justify-center p-5 text-sm text-slate-500">
        Select a monitor to view details
      </div>
    );
  }

  return (
    <div className="glass p-5">
      <div className="flex flex-col gap-1">
        <div className="flex items-center justify-between gap-3">
          <div className="flex min-w-0 items-center gap-2">
            <h3 className="truncate text-[15px] font-semibold text-white">{monitor.name}</h3>
            <Pill tone="neutral" size="xs">
              {monitorTypeLabel(monitor.type)} · {monitor.enabled ? 'Active' : 'Disabled'}
            </Pill>
          </div>
          <Pill tone={monitor.enabled ? 'success' : 'neutral'} size="xs" dot>
            {monitor.enabled ? 'Alerts enabled' : 'Alerts disabled'}
          </Pill>
        </div>
        <div className="text-xs text-slate-500">
          {monitor.tags && monitor.tags.length > 0
            ? `Tagged: ${monitor.tags.join(', ')}`
            : 'No tags'}
        </div>
      </div>

      {loading ? (
        <div className="flex h-48 items-center justify-center text-sm text-slate-500">
          Loading details...
        </div>
      ) : (
        <>
          <div className="mt-5">
            <h4 className={SECTION_TITLE}>Snapshot</h4>
            <div className="flex flex-wrap gap-2">
              <div className="glass-well min-w-[120px] flex-1 p-3">
                <div className="text-xs text-slate-400">Uptime 7 days</div>
                <div className="mt-1 text-base font-semibold tabular-nums text-white">{operationalCount > 0 ? `${uptime.toFixed(2)}%` : '—'}</div>
                <div className="mt-0.5 text-xs text-slate-500">
                  {operationalCount > 0 ? `${operationalCount} checks` : 'No data'}
                </div>
              </div>
              <div className="glass-well min-w-[120px] flex-1 p-3">
                <div className="text-xs text-slate-400">Latency</div>
                <div className="mt-1 text-base font-semibold tabular-nums text-white">P95: {latencyStats.p95} ms</div>
                <div className="mt-0.5 text-xs text-slate-500">
                  Median: {latencyStats.median} ms
                </div>
              </div>
              <div className="glass-well min-w-[120px] flex-1 p-3">
                <div className="text-xs text-slate-400">Last run</div>
                <div className="mt-1 text-base font-semibold tabular-nums text-white">
                  {latestOperationalResult
                    ? formatTimeAgo(latestOperationalResult.created_at)
                    : 'Never'}
                </div>
                <div className="mt-0.5 text-xs text-slate-500">
                  {latestOperationalResult?.http_status
                    ? `Status code: ${latestOperationalResult.http_status}`
                    : 'No status'}
                </div>
              </div>
              {latestTLS && (
                <div className="glass-well min-w-[120px] flex-1 p-3">
                  <div className="text-xs text-slate-400">Certificate</div>
                  <div className="mt-1 text-base font-semibold tabular-nums text-white">
                    {latestTLS.days_until_expiry !== undefined
                      ? `${latestTLS.days_until_expiry}d left`
                      : '—'}
                  </div>
                  <div className="mt-0.5 truncate text-xs text-slate-500">
                    {latestTLS.not_after
                      ? `Expires: ${new Date(latestTLS.not_after).toISOString().slice(0, 10)}`
                      : 'No TLS data'}
                  </div>
                </div>
              )}
            </div>
          </div>

          <div className="mt-5">
            <h4 className={SECTION_TITLE}>Configuration</h4>
            <ul className="space-y-2 text-[13px]">
              {monitor.type === 'prometheus' && (
                <li>
                  <PrometheusDetails monitor={monitor} result={checkResults[0]} size="compact" />
                </li>
              )}
              {monitor.type === 'http' && (() => {
                const httpConfig = getHTTPConfig(monitor);
                if (!httpConfig) return null;
                return (
                  <>
                    <li className="flex justify-between gap-3">
                      <span className="shrink-0 text-slate-400">URL</span>
                      <CopyableTarget
                        value={httpConfig.url}
                        label="URL"
                        size="sm"
                        className="min-w-0 justify-end"
                        textClassName="text-slate-200"
                      />
                    </li>
                    <li className="flex justify-between gap-3">
                      <span className="shrink-0 text-slate-400">Method</span>
                      <span className="text-right text-slate-200">
                        {httpConfig.method} · expect{' '}
                        {formatExpectedStatusRules(httpConfig)}
                      </span>
                    </li>
                    {(httpConfig.max_latency_ms ||
                      (httpConfig.body_assertions && httpConfig.body_assertions.length > 0) ||
                      (httpConfig.response_header_assertions && httpConfig.response_header_assertions.length > 0) ||
                      (httpConfig.json_assertions && httpConfig.json_assertions.length > 0)) && (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">Checks</span>
                        <span className="text-right text-slate-200">
                          {httpConfig.max_latency_ms ? `latency \u2264 ${httpConfig.max_latency_ms}ms` : 'latency default'}
                          {httpConfig.body_assertions?.length ? ` · body: ${httpConfig.body_assertions.length}` : ''}
                          {httpConfig.response_header_assertions?.length ? ` · headers: ${httpConfig.response_header_assertions.length}` : ''}
                          {httpConfig.json_assertions?.length ? ` · json: ${httpConfig.json_assertions.length}` : ''}
                        </span>
                      </li>
                    )}
                    {(httpConfig.follow_redirects !== undefined || httpConfig.max_redirects !== undefined) && (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">Redirects</span>
                        <span className="text-right text-slate-200">
                          {httpConfig.follow_redirects === false ? 'off' : `on · max ${httpConfig.max_redirects ?? 10}`}
                        </span>
                      </li>
                    )}
                    {(httpConfig.tls_min_days_valid !== undefined ||
                      httpConfig.tls_skip_verify ||
                      (httpConfig.tls_server_name && httpConfig.tls_server_name.trim())) && (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">TLS</span>
                        <span className="text-right text-slate-200">
                          {httpConfig.tls_skip_verify ? 'skip verify' : 'verify'}
                          {httpConfig.tls_min_days_valid !== undefined ? ` · min ${httpConfig.tls_min_days_valid}d` : ''}
                          {httpConfig.tls_server_name ? ` · sni ${httpConfig.tls_server_name}` : ''}
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
                  <li className="flex justify-between gap-3">
                    <span className="shrink-0 text-slate-400">Host</span>
                    <span className="truncate text-right text-slate-200">
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
                    <li className="flex justify-between gap-3">
                      <span className="shrink-0 text-slate-400">Host</span>
                      <span className="truncate text-right text-slate-200">
                        {dnsConfig.host}
                      </span>
                    </li>
                    <li className="flex justify-between gap-3">
                      <span className="shrink-0 text-slate-400">Record</span>
                      <span className="text-right text-slate-200">
                        {(dnsConfig.record_type || 'A').toUpperCase()}
                      </span>
                    </li>
                    {expected.length > 0 && (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">Expected</span>
                        <span className="truncate text-right text-slate-200">
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
                    <li className="flex justify-between gap-3">
                      <span className="shrink-0 text-slate-400">Target</span>
                      <span className="truncate text-right text-slate-200">
                        {grpcConfig.host}:{resolvedPort}
                      </span>
                    </li>
                    <li className="flex justify-between gap-3">
                      <span className="shrink-0 text-slate-400">TLS</span>
                      <span className="text-right text-slate-200">
                        {grpcConfig.use_tls === false ? 'disabled' : 'enabled'}
                      </span>
                    </li>
                    {grpcConfig.service && (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">Service</span>
                        <span className="truncate text-right text-slate-200">
                          {grpcConfig.service}
                        </span>
                      </li>
                    )}
                    {latestGRPCMetrics?.serving_status && (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">Last health</span>
                        <span className="text-right text-slate-200">
                          {latestGRPCMetrics.serving_status}
                        </span>
                      </li>
                    )}
                  </>
                );
              })()}
              {isDatabaseType(monitor.type) && (() => {
                const dbConfig = getDatabaseConfig(monitor);
                if (!dbConfig) return null;
                const defaultPort =
                  monitor.type === 'redis' ? 6379 : monitor.type === 'postgres' ? 5432 : monitor.type === 'rabbitmq' ? 5672 : 27017;
                const tls = monitor.type === 'postgres'
                  ? (dbConfig.ssl_mode || 'prefer')
                  : dbConfig.tls_enabled
                    ? `enabled${dbConfig.tls_skip_verify ? ' · skip verify' : ''}`
                    : 'disabled';
                return (
                  <>
                    <li className="flex justify-between gap-3">
                      <span className="shrink-0 text-slate-400">Target</span>
                      <span className="truncate text-right text-slate-200">
                        {dbConfig.connection_string
                          ? 'connection string (hidden)'
                          : `${dbConfig.host}:${dbConfig.port || defaultPort}`}
                      </span>
                    </li>
                    {!dbConfig.connection_string && (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">TLS</span>
                        <span className="text-right text-slate-200">{tls}</span>
                      </li>
                    )}
                    {dbConfig.username && (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">Auth</span>
                        <span className="truncate text-right text-slate-200">
                          {dbConfig.username}
                          {monitor.type === 'mongodb' ? ` @ ${dbConfig.auth_source || 'admin'}` : ''}
                        </span>
                      </li>
                    )}
                    {monitor.type === 'postgres' && (dbConfig.database || dbConfig.query) && (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">Check</span>
                        <span className="truncate text-right text-slate-200">
                          {dbConfig.database || 'postgres'}
                          {dbConfig.query ? ` · ${dbConfig.query}` : ' · ping'}
                        </span>
                      </li>
                    )}
                    {(dbConfig.max_latency_ms || dbConfig.warn_latency_ms) ? (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">Latency</span>
                        <span className="text-right text-slate-200">
                          {dbConfig.warn_latency_ms ? `warn > ${dbConfig.warn_latency_ms}ms` : ''}
                          {dbConfig.warn_latency_ms && dbConfig.max_latency_ms ? ' · ' : ''}
                          {dbConfig.max_latency_ms ? `fail > ${dbConfig.max_latency_ms}ms` : ''}
                        </span>
                      </li>
                    ) : null}
                    {latestDBMetrics && (latestDBMetrics.server_version || latestDBMetrics.role) && (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">Server</span>
                        <span className="truncate text-right text-slate-200">
                          {[
                            [latestDBMetrics.product, latestDBMetrics.server_version].filter(Boolean).join(' '),
                            latestDBMetrics.role,
                            latestDBMetrics.replica_set ? `set ${latestDBMetrics.replica_set}` : '',
                          ]
                            .filter(Boolean)
                            .join(' · ')}
                        </span>
                      </li>
                    )}
                    {latestDBMetrics?.latency_warn_ms ? (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-amber-300">Warning</span>
                        <span className="text-right text-amber-300">
                          last check exceeded {latestDBMetrics.latency_warn_ms}ms warn threshold
                        </span>
                      </li>
                    ) : null}
                    {monitor.type === 'mongodb' && (() => {
                      const mongo = latestDBMetrics as MongoDBMetrics | null;
                      if (!mongo) return null;
                      const repl = mongo.replication;
                      return (
                        <>
                          {repl && (
                            <li className="flex justify-between gap-3">
                              <span className="shrink-0 text-slate-400">Replication</span>
                              <span className="truncate text-right text-slate-200">
                                {repl.members_healthy}/{repl.members_total} healthy
                                {repl.max_lag_seconds != null ? ` · lag ${repl.max_lag_seconds.toFixed(1)}s` : ''}
                              </span>
                            </li>
                          )}
                          {mongo.replication_lag_warn_seconds != null && (
                            <li className="flex justify-between gap-3">
                              <span className="shrink-0 text-amber-300">Warning</span>
                              <span className="text-right text-amber-300">
                                replication lag over {mongo.replication_lag_warn_seconds}s warn threshold
                              </span>
                            </li>
                          )}
                          {mongo.unavailable?.some((u) => u.reason === 'unauthorized') && (
                            <li className="flex justify-between gap-3">
                              <span className="shrink-0 text-slate-400">Note</span>
                              <span className="truncate text-right text-slate-400">
                                Cluster checks skipped — grant clusterMonitor
                              </span>
                            </li>
                          )}
                        </>
                      );
                    })()}
                  </>
                );
              })()}
              {monitor.type === 'synthetic_api' && (() => {
                const synConfig = getSyntheticAPIConfig(monitor);
                if (!synConfig) return null;
                return (
                  <>
                    {synConfig.base_url && (
                      <li className="flex justify-between gap-3">
                        <span className="shrink-0 text-slate-400">Base URL</span>
                        <CopyableTarget
                          value={synConfig.base_url}
                          label="base URL"
                          size="sm"
                          className="min-w-0 justify-end"
                          textClassName="text-slate-200"
                        />
                      </li>
                    )}
                    <li className="flex justify-between gap-3">
                      <span className="shrink-0 text-slate-400">Workflow</span>
                      <span className="text-right text-slate-200">
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
                    <li className="flex justify-between gap-3">
                      <span className="shrink-0 text-slate-400">Start URL</span>
                      <CopyableTarget
                        value={synConfig.start_url}
                        label="start URL"
                        size="sm"
                        className="min-w-0 justify-end"
                        textClassName="text-slate-200"
                      />
                    </li>
                    <li className="flex justify-between gap-3">
                      <span className="shrink-0 text-slate-400">Journey</span>
                      <span className="text-right text-slate-200">
                        {(synConfig.steps || []).length} steps · {synConfig.device || 'default device'}
                      </span>
                    </li>
                  </>
                );
              })()}
              <li className="flex justify-between gap-3">
                <span className="shrink-0 text-slate-400">Schedule</span>
                <span className="text-right text-slate-200">
                  {formatInterval(monitor.interval_seconds)} · timeout{' '}
                  {monitor.timeout_seconds}s
                </span>
              </li>
              {monitor.tags && monitor.tags.length > 0 && (
                <li className="flex justify-between gap-3">
                  <span className="shrink-0 text-slate-400">Tags</span>
                  <span className="flex flex-wrap justify-end gap-1">
                    {monitor.tags.map((tag) => (
                      <Pill key={tag} tone="neutral" size="xs">{tag}</Pill>
                    ))}
                  </span>
                </li>
              )}
            </ul>
          </div>

          {monitor.type === 'synthetic_browser' && (
            <div className="mt-5">
              <h4 className={SECTION_TITLE}>Latest failure screenshot</h4>
              {screenshotLoading ? (
                <p className="text-[13px] text-slate-400">Loading screenshot...</p>
              ) : screenshotBlobURL ? (
                <div className="glass-well p-2">
                  <Image
                    src={screenshotBlobURL}
                    alt="Latest synthetic browser screenshot"
                    width={1200}
                    height={675}
                    unoptimized
                    loading="lazy"
                    className="h-auto max-h-[320px] w-full rounded-lg object-contain"
                  />
                  <div className="mt-2 flex items-center justify-between px-1 text-xs text-slate-500">
                    <span>{latestSyntheticBrowserResult ? formatTimeAgo(latestSyntheticBrowserResult.created_at) : 'Latest run'}</span>
                    <span>{latestSyntheticBrowserResult?.status || 'failure'}</span>
                  </div>
                  {latestSyntheticBrowserResult?.error_message && (
                    <p className="mt-1 px-1 text-xs text-rose-300">
                      {latestSyntheticBrowserResult.error_message}
                    </p>
                  )}
                </div>
              ) : (
                <p className="text-[13px] text-slate-400">
                  No screenshot available yet. A screenshot is captured on failed runs when enabled.
                </p>
              )}
            </div>
          )}

          <div className="mt-5">
            <h4 className={SECTION_TITLE}>Recent results</h4>
            {recentResults.length > 0 ? (
              <ul className="space-y-2 text-[13px]">
                {recentResults.map((result, idx) => (
                  <li key={result.id || idx} className="flex items-center justify-between gap-3">
                    <span className="flex min-w-0 items-center gap-1.5 text-slate-400">
                      {formatTimeAgo(result.created_at)}
                      {result.location_name && (
                        <Pill tone="neutral" size="xs">{result.location_name}</Pill>
                      )}
                    </span>
                    <span className="shrink-0 tabular-nums text-slate-200">
                      {result.http_status || 'N/A'} · {result.latency_ms || 0} ms ·{' '}
                      <span className={result.status === 'success' ? 'text-emerald-300' : 'text-rose-300'}>
                        {result.status}
                      </span>
                    </span>
                  </li>
                ))}
              </ul>
            ) : (
              <p className="text-[13px] text-slate-400">No check results yet</p>
            )}
          </div>
        </>
      )}
    </div>
  );
}
