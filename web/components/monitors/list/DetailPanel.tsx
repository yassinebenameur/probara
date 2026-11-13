'use client';

import PrometheusDetails from '@/components/monitors/PrometheusDetails';
import Link from 'next/link';
import { X } from 'lucide-react';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import IconButton from '@/components/ui/IconButton';
import { RelativeTime } from '@/components/ui/RelativeTime';
import { FOCUS_RING_INSET } from '@/components/ui/focus';
import TypeBadge from '@/components/ui/TypeBadge';
import CopyableTarget from '@/components/ui/CopyableTarget';
import AgentHostGlance from '@/components/monitors/AgentHostGlance';
import TagPill from '@/components/monitors/TagPill';
import { Monitor, CheckResult, AgentMonitorConfig } from '@/lib/types';
import { useSyntheticScreenshot } from '@/lib/useSyntheticScreenshot';
import { calculateUptime, countOperationalResults, displayValueLabel, formatInterval, getEffectiveMonitorStatus, getHTTPDisplayValues, isPlatformResult, monitorTarget, monitorTargetLabel, calculateLatencyStats, shortFailureReason } from '@/lib/monitor-utils';
import { formatLatency, formatUptime } from '@/lib/format';
import { CHECK_RESULT_BAR, MONITOR_STATUS_LABEL, MONITOR_STATUS_TONE } from '@/lib/status-tones';
import { RECENT_CHECKS } from '@/components/monitors/list/constants';
import { StatusDot } from '@/components/monitors/list/RowCells';

// Detail Panel Component
export function DetailPanel({
  monitor,
  results,
  members = [],
  memberResults = {},
  onSelectMember,
  onClose,
}: {
  monitor: Monitor | null;
  results: CheckResult[];
  members?: Monitor[];
  memberResults?: Record<string, CheckResult[]>;
  onSelectMember?: (id: string) => void;
  onClose?: () => void;
}) {

  const getSyntheticBrowserScreenshotPath = (result?: CheckResult): string | null => {
    const metrics = result?.metrics_data as { synthetic_browser?: { artifacts?: { screenshot_path?: string } } } | undefined;
    return metrics?.synthetic_browser?.artifacts?.screenshot_path || null;
  };

  const latestSyntheticBrowserResult = monitor?.type === 'synthetic_browser'
    ? results.find((result) => Boolean(getSyntheticBrowserScreenshotPath(result)))
    : undefined;
  const latestSyntheticBrowserScreenshotPath = getSyntheticBrowserScreenshotPath(latestSyntheticBrowserResult);
  const { blobUrl: screenshotBlobURL, loading: screenshotLoading } = useSyntheticScreenshot(
    monitor?.id,
    monitor?.tenant_id,
    latestSyntheticBrowserScreenshotPath
  );

  if (!monitor) {
    return (
      <div className="glass flex h-64 items-center justify-center p-6">
        <p className="text-sm text-slate-500">Select a monitor</p>
      </div>
    );
  }

  const status = getEffectiveMonitorStatus(monitor, results);
  const uptime = calculateUptime(results);
  const operationalCount = countOperationalResults(results);
  const latencyStats = calculateLatencyStats(results);
  const latestResult = results[0];
  const displayValues = getHTTPDisplayValues(results);
  const target = monitorTarget(monitor);

  const memberStatuses = monitor.type === 'group'
    ? members.map((m) => getEffectiveMonitorStatus(m, memberResults[m.id] || []))
    : [];
  const memberUp = memberStatuses.filter((s) => s === 'up').length;
  const memberDown = memberStatuses.filter((s) => s === 'down' || s === 'degraded').length;
  const memberPaused = memberStatuses.filter((s) => s === 'paused').length;
  const memberMaintenance = memberStatuses.filter((s) => s === 'maintenance').length;
  const memberOther = memberStatuses.length - memberUp - memberDown - memberPaused - memberMaintenance;

  const sectionTitle = 'mb-2 text-xs font-medium text-slate-400';

  return (
    <div className="glass">
      {/* Header */}
      <div className="flex items-start justify-between gap-3 px-5 pt-5">
        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-2">
            <h3 className="min-w-0 truncate text-title font-semibold text-white">{monitor.name}</h3>
            <Pill tone={MONITOR_STATUS_TONE[status]} size="xs" dot>{MONITOR_STATUS_LABEL[status]}</Pill>
          </div>
          <div className="mt-1 flex min-w-0 items-center gap-2">
            <TypeBadge type={monitor.type} config={monitor.config} />
            {target ? (
              <CopyableTarget
                value={target}
                label={monitorTargetLabel(monitor.type)}
                size="sm"
                textClassName="text-slate-500"
              />
            ) : (
              <span className="text-xs text-slate-500">{formatInterval(monitor.interval_seconds)}</span>
            )}
          </div>
        </div>
        {onClose && (
          <IconButton icon={<X strokeWidth={1.75} />} label="Close preview" onClick={onClose} />
        )}
      </div>

      <div className="space-y-5 p-5">
        {/* Stats: all three describe the last RECENT_CHECKS results the list fetched. */}
        <div>
          <div className="grid grid-cols-3 gap-2">
            <div className="glass-well px-3 py-2.5">
              <p className="text-xs text-slate-400">Success</p>
              <p className="mt-1 text-lg font-semibold tabular-nums text-white">{operationalCount > 0 ? formatUptime(uptime) : '—'}</p>
            </div>
            <div className="glass-well px-3 py-2.5">
              <p className="text-xs text-slate-400">Median</p>
              <p className="mt-1 text-lg font-semibold tabular-nums text-white">{latencyStats.samples ? formatLatency(latencyStats.median) : '—'}</p>
            </div>
            <div className="glass-well px-3 py-2.5">
              <p className="text-xs text-slate-400">p95</p>
              <p className="mt-1 text-lg font-semibold tabular-nums text-white">{latencyStats.samples ? formatLatency(latencyStats.p95) : '—'}</p>
            </div>
          </div>
          <p className="mt-2 text-2xs text-slate-500">
            Last {RECENT_CHECKS} checks; latency from successful checks only.
          </p>
        </div>

        {/* Response values (HTTP display_fields), shown in full here */}
        {displayValues.length > 0 && (
          <div>
            <h4 className={sectionTitle}>Response values</h4>
            <dl className="space-y-1.5 text-xs">
              {displayValues.map((v) => (
                <div key={v.path} className="flex items-baseline gap-2">
                  <dt className="shrink-0 text-slate-500" title={v.path}>{displayValueLabel(v) || v.path}</dt>
                  <dd className="min-w-0 break-all font-mono text-slate-300">{v.value}</dd>
                </div>
              ))}
            </dl>
          </div>
        )}

        {/* Group Members */}
        {monitor.type === 'group' && (
          <div>
            <h4 className={sectionTitle}>Members ({members.length})</h4>
            {members.length > 0 && (
              <>
                <div className="mb-1.5 flex h-1.5 overflow-hidden rounded-full bg-well-hover">
                  {memberUp > 0 && (
                    <div className="bg-emerald-500" style={{ width: `${(memberUp / members.length) * 100}%` }} />
                  )}
                  {memberDown > 0 && (
                    <div className="bg-rose-500" style={{ width: `${(memberDown / members.length) * 100}%` }} />
                  )}
                  {memberPaused > 0 && (
                    <div className="bg-slate-500" style={{ width: `${(memberPaused / members.length) * 100}%` }} />
                  )}
                  {memberMaintenance > 0 && (
                    <div className="bg-sky-500" style={{ width: `${(memberMaintenance / members.length) * 100}%` }} />
                  )}
                  {memberOther > 0 && (
                    <div className="bg-slate-500/50" style={{ width: `${(memberOther / members.length) * 100}%` }} />
                  )}
                </div>
                <p className="mb-2 flex flex-wrap gap-x-3 text-xs tabular-nums text-slate-500">
                  <span>{memberUp} up</span>
                  {memberDown > 0 && <span>{memberDown} down</span>}
                  {memberPaused > 0 && <span>{memberPaused} paused</span>}
                  {memberMaintenance > 0 && <span>{memberMaintenance} maintenance</span>}
                  {memberOther > 0 && <span>{memberOther} no data</span>}
                </p>
              </>
            )}
            {members.length > 0 ? (
              <div className="dashboard-scroll -mx-2 max-h-56 space-y-0.5 overflow-y-auto overscroll-contain ![scrollbar-gutter:auto]">
                {members.map((member, i) => {
                  const memberOperational = countOperationalResults(memberResults[member.id] || []);
                  const memberUptime = calculateUptime(memberResults[member.id] || []);
                  return (
                    <button
                      key={member.id}
                      onClick={() => onSelectMember?.(member.id)}
                      className={`flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-well active:bg-well-hover ${FOCUS_RING_INSET}`}
                    >
                      <StatusDot status={memberStatuses[i]} />
                      <span className="min-w-0 flex-1 truncate text-xs text-slate-300">{member.name}</span>
                      <span className="text-2xs tabular-nums text-slate-500">
                        {memberOperational > 0 ? formatUptime(memberUptime) : '—'}
                      </span>
                    </button>
                  );
                })}
              </div>
            ) : (
              <p className="text-xs text-slate-500">No members yet</p>
            )}
          </div>
        )}

        {monitor.type === 'agent' && (
          <AgentHostGlance
            monitorId={monitor.id}
            intervalSeconds={(monitor.config as AgentMonitorConfig | undefined)?.expected_interval_seconds ?? monitor.interval_seconds}
            metricRules={(monitor.config as AgentMonitorConfig | undefined)?.metric_rules}
            refreshKey={latestResult?.created_at}
            titleClassName={sectionTitle}
          />
        )}

        {/* Tags */}
        {monitor.tags && monitor.tags.length > 0 && (
          <div>
            <h4 className={sectionTitle}>Tags</h4>
            <div className="flex flex-wrap gap-1.5">
              {monitor.tags.map((tag) => (
                <TagPill key={tag} tag={tag} size="sm" />
              ))}
            </div>
          </div>
        )}

        {/* Config */}
        <div>
          <h4 className={sectionTitle}>Config</h4>
          <div className="space-y-1.5 text-xs">
            <div className="flex justify-between">
              <span className="text-slate-500">Interval</span>
              <span className="text-slate-300">{formatInterval(monitor.interval_seconds)}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-slate-500">Timeout</span>
              <span className="tabular-nums text-slate-300">{monitor.timeout_seconds}s</span>
            </div>
            <div className="flex justify-between">
              <span className="text-slate-500">Status</span>
              <span className={monitor.enabled ? 'text-slate-300' : 'text-slate-500'}>
                {monitor.enabled ? 'Active' : 'Paused'}
              </span>
            </div>
          </div>
        </div>

        {monitor.type === 'prometheus' && (
          <div>
            <h4 className={sectionTitle}>Prometheus query</h4>
            <PrometheusDetails monitor={monitor} result={results[0]} size="compact" />
          </div>
        )}

        {/* Synthetic Browser Screenshot */}
        {monitor.type === 'synthetic_browser' && (
          <div>
            <h4 className={sectionTitle}>Latest failure screenshot</h4>
            {screenshotLoading ? (
              <p className="text-xs text-slate-500">Loading screenshot…</p>
            ) : screenshotBlobURL ? (
              <div className="glass-well overflow-hidden p-2">
                {/* A blob: URL from an authenticated fetch, which next/image cannot load. */}
                {/* eslint-disable-next-line @next/next/no-img-element */}
                <img
                  src={screenshotBlobURL}
                  alt="Latest synthetic browser failure screenshot"
                  className="h-auto max-h-[220px] w-full rounded-lg object-contain"
                  loading="lazy"
                  decoding="async"
                />
                <div className="mt-2 text-2xs text-slate-500">
                  {latestSyntheticBrowserResult ? (
                    <RelativeTime value={latestSyntheticBrowserResult.created_at} />
                  ) : (
                    'Latest run'
                  )}
                </div>
              </div>
            ) : (
              <p className="text-xs text-slate-500">No screenshot available yet.</p>
            )}
          </div>
        )}

        {/* Recent Results: latency for passes, the short reason for failures. */}
        <div>
          <h4 className={sectionTitle}>Recent checks</h4>
          {results.length > 0 ? (
            <div className="space-y-1">
              {results.slice(0, 5).map((r) => {
                const platform = isPlatformResult(r);
                const reason = r.status === 'success' || platform
                  ? ''
                  : shortFailureReason(r.error_message, { httpStatus: r.http_status, timeoutSeconds: monitor.timeout_seconds });
                return (
                  <div key={r.id} className="flex items-center justify-between gap-3 py-1 text-xs">
                    <div className="flex min-w-0 items-center gap-2">
                      <span
                        className={`h-2 w-2 flex-shrink-0 rounded-full ${platform ? 'bg-slate-500' : CHECK_RESULT_BAR[r.status]}`}
                      />
                      <RelativeTime value={r.created_at} className="whitespace-nowrap text-slate-500" />
                    </div>
                    {reason ? (
                      <span className="min-w-0 truncate text-rose-300" title={r.error_message}>{reason}</span>
                    ) : (
                      <span className="tabular-nums text-slate-400">
                        {platform ? 'Not run' : formatLatency(r.latency_ms ?? null)}
                      </span>
                    )}
                  </div>
                );
              })}
            </div>
          ) : (
            <p className="py-2 text-center text-xs text-slate-500">No checks yet</p>
          )}
        </div>

        {/* View Details */}
        <Button variant="ghost" size="sm" className="w-full" asChild>
          <Link href={`/monitors/${monitor.id}`}>View details</Link>
        </Button>
      </div>
    </div>
  );
}
