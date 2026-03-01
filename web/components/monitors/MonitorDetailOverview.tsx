'use client';

import { CheckResult, Monitor, PushMetrics } from '@/lib/types';
import { calculateUptime, calculateLatencyStats, getOperationalResults } from '@/lib/monitor-utils';
import AgentMetricsView from './AgentMetricsView';
import HttpMonitorOverview from './HttpMonitorOverview';
import { UptimeHeroGauge, SLA_TARGET } from './UptimeHeroGauge';
import type { TimeRange } from './AgentMetricsView';

// Helper function to format metric names
function formatMetricName(name: string): string {
  return name
    .split('_')
    .map(word => {
      if (word === 'ms') return '(ms)';
      if (word === 'percent' || word === 'pct') return '%';
      if (['cpu', 'io', 'id', 'ip', 'url'].includes(word.toLowerCase())) return word.toUpperCase();
      return word.charAt(0).toUpperCase() + word.slice(1);
    })
    .join(' ')
    .replace(' (ms)', ' (ms)')
    .replace(' %', '%');
}

// Helper function to format metric values
function formatMetricValue(value: string | number | boolean): string {
  if (typeof value === 'boolean') return value ? 'Yes' : 'No';
  if (typeof value === 'number') {
    if (Number.isInteger(value)) return value.toString();
    return value.toFixed(2);
  }
  return String(value);
}

function getTimeAgo(dateStr: string): string {
  const diffSec = Math.floor((Date.now() - Date.parse(dateStr)) / 1000);
  if (diffSec < 60) return `${diffSec}s ago`;
  if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
  if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
  return `${Math.floor(diffSec / 86400)}d ago`;
}

// Push Metrics View component
function PushMetricsView({ results, loading }: { results: CheckResult[]; loading?: boolean }) {
  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="text-slate-500">Loading push metrics...</div>
      </div>
    );
  }

  const latestWithMetrics = results.find(r => r.metrics_data && Object.keys(r.metrics_data).length > 0);
  const metrics = latestWithMetrics?.metrics_data as PushMetrics | undefined;
  const operationalResults = getOperationalResults(results);
  const operationalCount = operationalResults.length;
  const successCount = operationalResults.filter(r => r.status === 'success').length;
  const latestResult = operationalResults[0];
  const uptime = calculateUptime(results);
  const downtimePct = operationalCount > 0 ? 100 - uptime : 0;
  const metricsCount = metrics ? Object.keys(metrics).length : 0;

  return (
    <div className="space-y-6">
      {/* ── Hero: Uptime Gauge ─────────────────────────────────────────────── */}
      <div className="relative rounded-2xl border border-white/[0.06] bg-slate-900/60 backdrop-blur-sm overflow-hidden">
        <div
          className="pointer-events-none absolute inset-0 rounded-2xl"
          style={{
            background: uptime >= SLA_TARGET
              ? 'radial-gradient(ellipse 60% 60% at 50% 40%, rgba(6,182,212,0.08), transparent)'
              : 'radial-gradient(ellipse 60% 60% at 50% 40%, rgba(244,63,94,0.08), transparent)',
          }}
        />
        <UptimeHeroGauge uptime={uptime} hasData={operationalCount > 0} />

        {/* Last push strip */}
        {latestResult && (
          <div className="mx-6 mb-6 flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-950/40 px-4 py-2.5">
            <div className="flex items-center gap-2.5">
              <span className={`h-2 w-2 rounded-full ${latestResult.status === 'success' ? 'bg-emerald-500' : 'bg-rose-500'}`} />
              <span className="text-xs text-slate-400">
                Last push <span className="text-slate-300">{getTimeAgo(latestResult.created_at)}</span>
              </span>
            </div>
            <span className={`rounded px-2 py-0.5 text-xs font-medium ${
              latestResult.status === 'success' ? 'bg-emerald-500/10 text-emerald-400' : 'bg-rose-500/10 text-rose-400'
            }`}>
              {latestResult.status === 'success' ? 'UP' : 'DOWN'}
            </span>
          </div>
        )}
      </div>

      {/* ── Secondary stats row ────────────────────────────────────────────── */}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-xl border border-violet-500/15 bg-violet-500/[0.04] p-4">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-slate-500">Total Pushes</p>
          <p className="mt-2 font-mono text-xl font-bold text-white">{operationalCount}</p>
          <p className="mt-1 text-xs text-slate-500">
            <span className="text-emerald-400">{successCount}</span> successful
          </p>
        </div>
        <div className="rounded-xl border border-cyan-500/15 bg-cyan-500/[0.04] p-4">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-slate-500">Auto Metrics</p>
          <p className="mt-2 font-mono text-xl font-bold text-white">{metricsCount}</p>
          <p className="mt-1 text-xs text-slate-500">From latest push</p>
        </div>
        <div className="rounded-xl border border-white/[0.06] bg-slate-800/30 p-4">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-slate-500">Success Rate</p>
          <p className="mt-2 font-mono text-xl font-bold text-emerald-400">
            {operationalCount > 0 ? `${((successCount / operationalCount) * 100).toFixed(1)}%` : 'N/A'}
          </p>
          <p className="mt-1 text-xs text-slate-500">{operationalCount - successCount} failed</p>
        </div>
        <div className="rounded-xl border border-rose-500/15 bg-rose-500/[0.04] p-4">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-slate-500">Downtime</p>
          <p className={`mt-2 font-mono text-xl font-bold ${downtimePct > 0 ? 'text-rose-400' : 'text-emerald-400'}`}>
            {operationalCount > 0 ? `${downtimePct.toFixed(3)}%` : 'N/A'}
          </p>
          <p className="mt-1 text-xs text-slate-500">
            {downtimePct === 0 ? 'No downtime recorded' : `${operationalCount - successCount} missed pushes`}
          </p>
        </div>
      </div>

      {/* Push Metrics Grid (Auto-detected) */}
      {metrics && Object.keys(metrics).length > 0 && (
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
          <div className="flex items-center justify-between mb-4">
            <div>
              <h3 className="text-sm font-medium text-white">Latest Push Metrics</h3>
              <p className="text-xs text-slate-500 mt-0.5">Auto-detected from incoming data</p>
            </div>
            <span className="px-2 py-0.5 text-xs rounded bg-purple-500/10 text-purple-400">
              {Object.keys(metrics).length} metrics
            </span>
          </div>
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {Object.entries(metrics).map(([key, value]) => (
              <div key={key} className="rounded-lg border border-white/[0.06] bg-slate-800/30 px-4 py-3">
                <p className="text-xs text-slate-500">{formatMetricName(key)}</p>
                <p className="mt-1 text-lg font-semibold text-white">{formatMetricValue(value)}</p>
              </div>
            ))}
          </div>
        </div>
      )}

      {!metrics && (
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
          <div className="flex flex-col items-center justify-center py-8 text-center">
            <svg className="h-12 w-12 text-slate-600 mb-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-8l-4-4m0 0L8 8m4-4v12" />
            </svg>
            <h3 className="text-sm font-medium text-white mb-1">Awaiting metrics</h3>
            <p className="text-xs text-slate-500 max-w-sm">
              No custom metrics have been received yet. Send metrics with your push request
              and they will automatically appear here.
            </p>
          </div>
        </div>
      )}

      {/* Recent Pushes Table */}
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 overflow-hidden">
        <div className="px-5 py-4 border-b border-white/[0.06]">
          <h3 className="text-sm font-medium text-white">Recent Pushes</h3>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-white/[0.06] bg-slate-800/30">
                <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Time (UTC)</th>
                <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Status</th>
                <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Metrics</th>
                <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Error</th>
              </tr>
            </thead>
            <tbody>
              {results.slice(0, 10).map((result, idx) => {
                const time = new Date(result.created_at).toISOString().replace('T', ' ').slice(0, 19);
                const mc = result.metrics_data ? Object.keys(result.metrics_data).length : 0;
                return (
                  <tr key={result.id} className={`border-b border-white/[0.03] ${idx % 2 === 1 ? 'bg-slate-800/20' : ''}`}>
                    <td className="px-5 py-3 text-sm font-mono text-slate-400">{time}</td>
                    <td className="px-5 py-3">
                      <span className={`inline-flex items-center gap-1.5 text-xs ${result.status === 'success' ? 'text-emerald-400' : 'text-rose-400'}`}>
                        <span className={`h-1.5 w-1.5 rounded-full ${result.status === 'success' ? 'bg-emerald-500' : 'bg-rose-500'}`} />
                        {result.status === 'success' ? 'UP' : 'DOWN'}
                      </span>
                    </td>
                    <td className="px-5 py-3 text-sm text-slate-400">{mc > 0 ? `${mc} metrics` : '—'}</td>
                    <td className="px-5 py-3 text-sm text-slate-400">{result.error_message || '—'}</td>
                  </tr>
                );
              })}
              {results.length === 0 && (
                <tr>
                  <td colSpan={4} className="px-5 py-8 text-center text-sm text-slate-500">No push results available yet</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

interface MonitorDetailOverviewProps {
  monitor: Monitor;
  results: CheckResult[];
  loading?: boolean;
  agentTimeRange?: TimeRange;
  onAgentTimeRangeChange?: (range: TimeRange) => void;
  timeRange?: TimeRange;
  onTimeRangeChange?: (range: TimeRange) => void;
}

// Mini bar chart for response times
function ResponseTimeBars({ results }: { results: CheckResult[] }) {
  const recentResults = getOperationalResults(results).slice(0, 30).reverse();
  const maxLatency = Math.max(...recentResults.map(r => r.latency_ms || 0), 1);

  if (recentResults.length === 0) {
    return (
      <div className="flex items-center justify-center h-20 text-sm text-slate-500">
        No data yet
      </div>
    );
  }

  return (
    <div className="flex items-end gap-0.5 h-20">
      {recentResults.map((result, i) => {
        const height = ((result.latency_ms || 0) / maxLatency) * 100;
        const isSuccess = result.status === 'success';
        return (
          <div
            key={result.id || i}
            className="flex-1 rounded-t transition-all hover:opacity-80"
            style={{
              height: `${Math.max(height, 4)}%`,
              backgroundColor: isSuccess ? 'rgb(34, 197, 94)' : 'rgb(239, 68, 68)',
              opacity: 0.8,
            }}
            title={`${result.latency_ms || 0}ms - ${result.status}`}
          />
        );
      })}
    </div>
  );
}

function extractSyntheticInsights(monitor: Monitor, latestResult?: CheckResult) {
  if (!latestResult?.metrics_data || (monitor.type !== 'synthetic_api' && monitor.type !== 'synthetic_browser')) {
    return null;
  }

  const metrics = latestResult.metrics_data as {
    synthetic_api?: { completed_steps?: number; failed_step_id?: string; total_latency_ms?: number };
    synthetic_browser?: {
      completed_steps?: number;
      failed_step_id?: string;
      total_latency_ms?: number;
      final_url?: string;
      start_url?: string;
      device?: string;
    };
  };

  if (monitor.type === 'synthetic_api' && metrics.synthetic_api) {
    return {
      completedSteps: metrics.synthetic_api.completed_steps,
      failedStepId: metrics.synthetic_api.failed_step_id,
      totalLatencyMs: metrics.synthetic_api.total_latency_ms,
      finalUrl: undefined as string | undefined,
      startUrl: undefined as string | undefined,
      device: undefined as string | undefined,
    };
  }

  if (monitor.type === 'synthetic_browser' && metrics.synthetic_browser) {
    return {
      completedSteps: metrics.synthetic_browser.completed_steps,
      failedStepId: metrics.synthetic_browser.failed_step_id,
      totalLatencyMs: metrics.synthetic_browser.total_latency_ms,
      finalUrl: metrics.synthetic_browser.final_url,
      startUrl: metrics.synthetic_browser.start_url,
      device: metrics.synthetic_browser.device,
    };
  }

  return null;
}

export default function MonitorDetailOverview({
  monitor,
  results,
  loading = false,
  agentTimeRange = '24h',
  onAgentTimeRangeChange,
  timeRange,
  onTimeRangeChange,
}: MonitorDetailOverviewProps) {
  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="text-slate-500">Loading monitor data...</div>
      </div>
    );
  }

  // For agent monitors, show the agent metrics view
  if (monitor.type === 'agent') {
    return (
      <AgentMetricsView
        results={results}
        loading={loading}
        timeRange={agentTimeRange}
        onTimeRangeChange={onAgentTimeRangeChange}
      />
    );
  }

  // For HTTP monitors, show the rich HTTP-specific overview
  if (monitor.type === 'http') {
    return (
      <HttpMonitorOverview
        monitor={monitor}
        results={results}
        loading={loading}
        timeRange={timeRange}
        onTimeRangeChange={onTimeRangeChange}
      />
    );
  }

  // For push monitors, show the push metrics view
  if (monitor.type === 'push') {
    return <PushMetricsView results={results} loading={loading} />;
  }

  const uptime = calculateUptime(results);
  const latencyStats = calculateLatencyStats(results);
  const operationalResults = getOperationalResults(results);
  const operationalCount = operationalResults.length;
  const successCount = operationalResults.filter(r => r.status === 'success').length;
  const latestResult = operationalResults[0];
  const syntheticInsights = extractSyntheticInsights(monitor, latestResult);
  const downtimePct = operationalCount > 0 ? 100 - uptime : 0;

  const formatLatency = (ms: number | null | undefined): string => {
    if (!ms) return '—';
    return ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${ms.toFixed(0)}ms`;
  };

  return (
    <div className="space-y-6">
      {/* ── Hero: Uptime Gauge ─────────────────────────────────────────────── */}
      <div className="relative rounded-2xl border border-white/[0.06] bg-slate-900/60 backdrop-blur-sm overflow-hidden">
        <div
          className="pointer-events-none absolute inset-0 rounded-2xl"
          style={{
            background: uptime >= SLA_TARGET
              ? 'radial-gradient(ellipse 60% 60% at 50% 40%, rgba(6,182,212,0.08), transparent)'
              : 'radial-gradient(ellipse 60% 60% at 50% 40%, rgba(244,63,94,0.08), transparent)',
          }}
        />
        <UptimeHeroGauge uptime={uptime} hasData={operationalCount > 0} />

        {/* Last check strip */}
        {latestResult && (
          <div className="mx-6 mb-6 flex items-center justify-between rounded-lg border border-white/[0.06] bg-slate-950/40 px-4 py-2.5">
            <div className="flex items-center gap-2.5">
              <span className={`h-2 w-2 rounded-full ${
                latestResult.status === 'success' ? 'bg-emerald-500'
                : latestResult.status === 'degraded' ? 'bg-amber-500' : 'bg-rose-500'
              }`} />
              <span className="text-xs text-slate-400">
                Last check <span className="text-slate-300">{getTimeAgo(latestResult.created_at)}</span>
              </span>
            </div>
            <div className="flex items-center gap-4">
              {latestResult.latency_ms ? (
                <span className="font-mono text-xs text-slate-400">{formatLatency(latestResult.latency_ms)}</span>
              ) : null}
              {latestResult.http_status ? (
                <span className={`font-mono text-xs ${latestResult.http_status < 400 ? 'text-emerald-400' : 'text-rose-400'}`}>
                  HTTP {latestResult.http_status}
                </span>
              ) : null}
            </div>
          </div>
        )}
      </div>

      {/* ── Secondary stats row ────────────────────────────────────────────── */}
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <div className="rounded-xl border border-cyan-500/15 bg-cyan-500/[0.04] p-4">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-slate-500">P95 Latency</p>
          <p className="mt-2 font-mono text-xl font-bold text-white">
            {latencyStats.p95 ? formatLatency(latencyStats.p95) : 'N/A'}
          </p>
          <p className="mt-1 text-xs text-slate-500">
            Median: <span className="font-mono text-slate-400">{latencyStats.median ? formatLatency(latencyStats.median) : 'N/A'}</span>
          </p>
        </div>
        <div className="rounded-xl border border-violet-500/15 bg-violet-500/[0.04] p-4">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-slate-500">Latest Response</p>
          <p className="mt-2 font-mono text-xl font-bold text-white">
            {formatLatency(latestResult?.latency_ms)}
          </p>
          <p className="mt-1 text-xs text-slate-500">
            {latestResult?.http_status ? (
              <span className={`font-mono ${latestResult.http_status < 400 ? 'text-emerald-400' : 'text-rose-400'}`}>
                HTTP {latestResult.http_status}
              </span>
            ) : '—'}
          </p>
        </div>
        <div className="rounded-xl border border-white/[0.06] bg-slate-800/30 p-4">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-slate-500">Total Checks</p>
          <p className="mt-2 font-mono text-xl font-bold text-white">{operationalCount}</p>
          <p className="mt-1 text-xs text-slate-500">
            <span className="text-emerald-400">{successCount}</span> successful
          </p>
        </div>
        <div className="rounded-xl border border-rose-500/15 bg-rose-500/[0.04] p-4">
          <p className="text-[10px] font-semibold uppercase tracking-wider text-slate-500">Downtime</p>
          <p className={`mt-2 font-mono text-xl font-bold ${downtimePct > 0 ? 'text-rose-400' : 'text-emerald-400'}`}>
            {operationalCount > 0 ? `${downtimePct.toFixed(3)}%` : 'N/A'}
          </p>
          <p className="mt-1 text-xs text-slate-500">
            {downtimePct === 0 ? 'No downtime recorded' : `${operationalCount - successCount} failed checks`}
          </p>
        </div>
      </div>

      {/* Synthetic journey insights panel */}
      {syntheticInsights && (
        <div className="rounded-xl border border-cyan-500/20 bg-cyan-500/5 p-5">
          <div className="flex items-center justify-between">
            <div>
              <h3 className="text-sm font-medium text-white">Journey Insights</h3>
              <p className="text-xs text-slate-500 mt-0.5">Quick context from the latest synthetic run</p>
            </div>
            <span className="rounded-full border border-cyan-400/30 bg-slate-900/50 px-2 py-1 text-[11px] text-cyan-300 uppercase tracking-wide">
              {monitor.type === 'synthetic_api' ? 'Synthetic API' : 'Synthetic Browser'}
            </span>
          </div>
          <div className="mt-4 grid gap-3 sm:grid-cols-2">
            <div className="rounded-lg border border-white/[0.08] bg-slate-900/50 px-3 py-2">
              <p className="text-[11px] text-slate-500">Completed steps</p>
              <p className="mt-1 text-sm font-medium text-white">
                {syntheticInsights.completedSteps !== undefined ? syntheticInsights.completedSteps : 'N/A'}
              </p>
            </div>
            <div className="rounded-lg border border-white/[0.08] bg-slate-900/50 px-3 py-2">
              <p className="text-[11px] text-slate-500">Failed step</p>
              <p className="mt-1 text-sm font-medium text-white">{syntheticInsights.failedStepId || 'None'}</p>
            </div>
            <div className="rounded-lg border border-white/[0.08] bg-slate-900/50 px-3 py-2">
              <p className="text-[11px] text-slate-500">Total latency</p>
              <p className="mt-1 text-sm font-medium text-white">
                {syntheticInsights.totalLatencyMs !== undefined ? `${syntheticInsights.totalLatencyMs}ms` : 'N/A'}
              </p>
            </div>
            {monitor.type === 'synthetic_browser' && (
              <div className="rounded-lg border border-white/[0.08] bg-slate-900/50 px-3 py-2">
                <p className="text-[11px] text-slate-500">Final URL</p>
                <p className="mt-1 truncate text-sm font-medium text-white">{syntheticInsights.finalUrl || 'N/A'}</p>
              </div>
            )}
          </div>
        </div>
      )}

      {/* Response Time Chart */}
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-5">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-sm font-medium text-white">Response Times</h3>
            <p className="text-xs text-slate-500 mt-0.5">Last 30 checks</p>
          </div>
          <div className="flex items-center gap-4 text-xs text-slate-500">
            <span className="flex items-center gap-1"><span className="h-2 w-2 rounded-full bg-emerald-500" />Success</span>
            <span className="flex items-center gap-1"><span className="h-2 w-2 rounded-full bg-rose-500" />Failed</span>
          </div>
        </div>
        <ResponseTimeBars results={results} />
        <div className="flex justify-between mt-2 text-xs text-slate-500">
          <span>30 checks ago</span>
          <span>Now</span>
        </div>
      </div>

      {/* Recent Results Table */}
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 overflow-hidden">
        <div className="px-5 py-4 border-b border-white/[0.06]">
          <h3 className="text-sm font-medium text-white">Recent Results</h3>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-white/[0.06] bg-slate-800/30">
                <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Time (UTC)</th>
                <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Status</th>
                <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Code</th>
                <th className="px-5 py-3 text-left text-xs font-medium text-slate-500">Latency</th>
              </tr>
            </thead>
            <tbody>
              {results.slice(0, 10).map((result, idx) => {
                const time = new Date(result.created_at).toISOString().replace('T', ' ').slice(0, 19);
                return (
                  <tr key={result.id} className={`border-b border-white/[0.03] ${idx % 2 === 1 ? 'bg-slate-800/20' : ''}`}>
                    <td className="px-5 py-3 text-sm font-mono text-slate-400">{time}</td>
                    <td className="px-5 py-3">
                      <span className={`inline-flex items-center gap-1.5 text-xs ${result.status === 'success' ? 'text-emerald-400' : 'text-rose-400'}`}>
                        <span className={`h-1.5 w-1.5 rounded-full ${result.status === 'success' ? 'bg-emerald-500' : 'bg-rose-500'}`} />
                        {result.status === 'success' ? 'OK' : 'Failed'}
                      </span>
                    </td>
                    <td className="px-5 py-3 text-sm text-slate-400">{result.http_status || 'N/A'}</td>
                    <td className="px-5 py-3 text-sm text-slate-400">{formatLatency(result.latency_ms)}</td>
                  </tr>
                );
              })}
              {results.length === 0 && (
                <tr>
                  <td colSpan={4} className="px-5 py-8 text-center text-sm text-slate-500">No check results available yet</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Tags */}
      {monitor.tags && monitor.tags.length > 0 && (
        <div className="flex items-center gap-2">
          <span className="text-xs text-slate-500">Tags:</span>
          <div className="flex flex-wrap gap-1.5">
            {monitor.tags.map((tag) => (
              <span key={tag} className="badge badge-default text-xs">{tag}</span>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
