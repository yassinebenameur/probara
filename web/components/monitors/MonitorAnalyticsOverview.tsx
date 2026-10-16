'use client';

import { ReactNode, useMemo } from 'react';
import {
  Area,
  AreaChart,
  ReferenceArea,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import Link from 'next/link';

import { CheckResult, Monitor, MonitorAnalyticsResponse, MonitorAnalyticsRange } from '@/lib/types';
import { collapseResultRuns, describeRun } from '@/lib/result-runs';
import { formatTarget, type Sla } from '@/lib/sla';
import {
  formatDate,
  formatDateTime,
  formatDateTimeFull,
  formatDuration,
  formatLatency,
  formatTimestamp,
  formatUptime,
  pluralize,
  RANGES,
  timezoneLabel,
} from '@/lib/format';
import {
  checkResultLabel,
  checkResultTone,
  failedChecksTone,
  UPTIME_TONE_BAR,
  UPTIME_TONE_TEXT,
  uptimeTone,
} from '@/lib/status-tones';
import { isPlatformResult, shortFailureReason } from '@/lib/monitor-utils';
import Pill from '@/components/ui/Pill';
import { CHART_AXIS_TICK, CHART_CURSOR, CHART_TOOLTIP_CLASS, isolatedDot } from './metric-chart';

// Latency is not a status, so it takes a calm series colour, never the
// orange reserved for actions.
const LATENCY_COLOR = '#6fb5dd';

const isIntraday = (range: MonitorAnalyticsRange) => range === '1h' || range === '6h' || range === '24h';

/** `15:19` for intraday buckets, `2 Oct` for daily ones. */
function formatBucketLabel(value: number, range: MonitorAnalyticsRange): string {
  return isIntraday(range) ? formatTimestamp(value, { seconds: false }).slice(11) : formatDate(value);
}

/** Bucket heading for tooltips: `2 Oct, 15:00` or `2 Oct`. */
function formatBucketTitle(value: number, range: MonitorAnalyticsRange): string {
  return isIntraday(range) ? formatDateTime(value) : formatDate(value);
}

// ── Stat strip ───────────────────────────────────────────────────────────

/**
 * One headline number: muted label, 22px value, one caption line. Cells sit
 * side by side in a StatStrip, divided by hairlines.
 */
export function Stat({
  label,
  value,
  valueClassName = 'text-white',
  caption,
}: {
  label: ReactNode;
  value: ReactNode;
  valueClassName?: string;
  caption?: ReactNode;
}) {
  return (
    <div className="min-w-0 px-5 py-4">
      <dt className="text-xs text-slate-400">{label}</dt>
      <dd className={`mt-1 text-[22px] font-semibold leading-7 tabular-nums ${valueClassName}`}>{value}</dd>
      {caption && <dd className="mt-0.5 text-xs leading-4 text-slate-500">{caption}</dd>}
    </div>
  );
}

/** A row of Stats in one glass panel; two per row on phones. */
export function StatStrip({ children, footer }: { children: ReactNode; footer?: ReactNode }) {
  return (
    <section className="glass overflow-hidden">
      <dl className="grid grid-cols-2 [&>*]:border-divider sm:flex sm:[&>*]:flex-1 sm:[&>*+*]:border-l max-sm:[&>*:nth-child(even)]:border-l max-sm:[&>*:nth-child(n+3)]:border-t max-sm:[&>*:last-child:nth-child(odd)]:col-span-2">
        {children}
      </dl>
      {footer && <div className="border-t border-divider px-5 pb-4 pt-3.5">{footer}</div>}
    </section>
  );
}

// ── Uptime trace ─────────────────────────────────────────────────────────

type TraceBucket = { ts: number; pct: number | null; checks: number; failed: number };

/** Failed checks in a bucket, from its success ratio (whole checks). */
export function bucketFailedChecks(checks: number, pct: number | null): number {
  if (pct === null || checks <= 0) return 0;
  return Math.max(0, Math.min(checks, Math.round(checks * (1 - pct / 100))));
}

/**
 * The signature element: one bar per analytics bucket, muted where the
 * bucket has no checks. A bucket's ratio is a check success ratio, not
 * availability (one failing location of three, or one flaky check, leaves
 * the monitor available), so bars take failedChecksTone, never the
 * availability target the headline uses. A single bucket and a 0% bucket
 * both stay visible, which an area chart pinned to its baseline does not.
 */
function UptimeTrace({
  buckets,
  range,
  windowStart,
  windowEnd,
}: {
  buckets: TraceBucket[];
  range: MonitorAnalyticsRange;
  windowStart: number | null;
  windowEnd: number;
}) {
  const gap = buckets.length <= 48 ? 3 : buckets.length <= 120 ? 1 : 0;
  const withFailures = buckets.filter((b) => b.failed > 0).length;
  const empty = buckets.filter((b) => b.pct === null).length;
  const bucketMs = buckets.length > 1 ? buckets[1].ts - buckets[0].ts : null;
  const bucketSpan = bucketMs !== null && bucketMs > 0 ? formatDuration(bucketMs / 1000) : null;
  const summary = `${pluralize(buckets.length, 'bucket')}, ${withFailures} with failed checks${empty ? `, ${empty} without checks` : ''}`;
  return (
    <div>
      <div role="img" aria-label={`Checks per bucket: ${summary}`} className="flex h-8 items-stretch" style={{ gap }}>
        {buckets.map((bucket, index) => {
          const end = buckets[index + 1]?.ts ?? windowEnd;
          const span = `${formatBucketTitle(bucket.ts, range)} – ${
            isIntraday(range) ? formatTimestamp(end, { seconds: false }).slice(11) : formatBucketTitle(end, range)
          }`;
          const title =
            bucket.pct === null
              ? `${span}: no checks`
              : `${span}: ${bucket.failed.toLocaleString()} of ${pluralize(bucket.checks, 'check')} failed`;
          return (
            <span
              key={bucket.ts}
              title={title}
              className={`min-w-0 flex-1 rounded-[2px] transition-opacity hover:opacity-70 ${
                bucket.pct === null ? 'bg-slate-500/20' : UPTIME_TONE_BAR[failedChecksTone(bucket.failed, bucket.checks)]
              }`}
            />
          );
        })}
      </div>
      <div className="mt-1.5 flex justify-between gap-3 text-2xs tabular-nums text-slate-500">
        <span>{windowStart !== null ? formatBucketTitle(windowStart, range) : ''}</span>
        {bucketSpan && <span className="hidden sm:inline">Checks per {bucketSpan === '1d' ? 'day' : bucketSpan === '1h' ? 'hour' : bucketSpan}</span>}
        <span>now</span>
      </div>
    </div>
  );
}

// ── Latency chart ────────────────────────────────────────────────────────

function LatencyTooltip({
  active,
  payload,
  label,
  range,
}: {
  active?: boolean;
  payload?: Array<{ value?: unknown }>;
  label?: number;
  range: MonitorAnalyticsRange;
}) {
  if (!active || typeof label !== 'number') return null;
  const value = payload?.find((entry) => typeof entry.value === 'number')?.value as number | undefined;
  return (
    <div className={CHART_TOOLTIP_CLASS}>
      <p className="text-xs text-slate-400">{formatBucketTitle(label, range)}</p>
      <p className="mt-1 text-sm font-medium tabular-nums">
        {value === undefined ? 'No successful checks' : formatLatency(value)}
      </p>
    </div>
  );
}

// ── Latest checks ────────────────────────────────────────────────────────

// Monitors without a response time of their own: a group rolls up its
// members, a push monitor is a heartbeat sent to us. Both skip the latency
// stat, the response-time chart and the Latency column.
const measuresLatency = (type: string) => type !== 'group' && type !== 'push';

const RESULT_SOURCE_TITLE = 'Recorded by the platform, not by a check (for example, the job expired before a worker ran it)';

function SourceTag({ result }: { result: CheckResult }) {
  if (!isPlatformResult(result)) return null;
  return (
    <span title={RESULT_SOURCE_TITLE}>
      <Pill size="xs">platform</Pill>
    </span>
  );
}

function Reason({ result, timeoutSeconds }: { result: CheckResult; timeoutSeconds: number }) {
  if (result.status === 'success') return null;
  const reason = shortFailureReason(result.error_message, {
    httpStatus: result.http_status,
    timeoutSeconds,
  });
  if (!reason) return null;
  return (
    <span className="block truncate" title={result.error_message || reason}>
      {reason}
    </span>
  );
}

function RunTime({ span, truncated }: { span: ReturnType<typeof describeRun>; truncated: boolean }) {
  return (
    <span title={truncated ? 'This run started before the oldest check fetched' : undefined}>
      {span.start}
      {span.end && (
        <>
          <span className="text-slate-500"> – </span>
          {span.end}
        </>
      )}
    </span>
  );
}

/**
 * The newest checks, independent of the time range. Consecutive checks with
 * the same outcome fold into a run; the oldest run is marked as a lower bound
 * when the page was cut off by `fetchLimit`.
 */
function LatestChecks({
  results,
  monitor,
  fetchLimit,
}: {
  results: CheckResult[];
  monitor: Monitor;
  fetchLimit: number;
}) {
  const allRuns = collapseResultRuns(results);
  const runs = allRuns.slice(0, 10);
  const pageFull = results.length >= fetchLimit;
  const showLocation = (monitor.locations?.length ?? 0) > 0;
  // A group's results are its members' checks (unnamed here) plus its own
  // roll-up records, so its caption says so.
  const isGroup = monitor.type === 'group';
  const showLatency = measuresLatency(monitor.type);
  const rows = runs.map((run, index) => {
    const truncated = pageFull && index === allRuns.length - 1;
    return { run, truncated, span: describeRun(run, { truncated }) };
  });

  return (
    <section className="glass overflow-hidden">
      <div className="px-5 pb-3 pt-4">
        <h3 className="text-title font-semibold text-white">Latest checks</h3>
        <p className="mt-0.5 text-xs text-slate-500">
          {isGroup
            ? `The last ${fetchLimit} checks across the group's members, whatever the time range. Repeats are folded into one row.`
            : `The last ${fetchLimit} checks, whatever the time range. Repeats are folded into one row.`}
        </p>
      </div>

      {rows.length === 0 ? (
        <p className="border-t border-divider px-5 py-8 text-center text-sm text-slate-500">No checks recorded yet.</p>
      ) : (
        <>
          {/* Phones: one stacked row per run instead of a sideways-scrolling table. */}
          <ul className="divide-y divide-divider border-t border-divider sm:hidden">
            {rows.map(({ run, span, truncated }) => (
              <li key={run.latest.id} className="space-y-1 px-5 py-3">
                <div className="flex items-center gap-2">
                  <Pill size="xs" tone={checkResultTone(run.latest.status)}>
                    {checkResultLabel(run.latest.status)}
                  </Pill>
                  {span.count && <span className="text-xs tabular-nums text-slate-500">{span.count}</span>}
                  <SourceTag result={run.latest} />
                  {showLocation && run.latest.location_name && <Pill size="xs">{run.latest.location_name}</Pill>}
                  {showLatency && (
                    <span className="ml-auto text-ui tabular-nums text-slate-300">
                      {run.latest.status === 'success' ? formatLatency(run.latest.latency_ms) : ''}
                    </span>
                  )}
                </div>
                <p className="font-mono text-2xs text-slate-400">
                  <RunTime span={span} truncated={truncated} />
                  {span.duration && <span className="ml-2 font-sans text-slate-500">{span.duration}</span>}
                </p>
                <div className="text-xs text-slate-400">
                  <Reason result={run.latest} timeoutSeconds={monitor.timeout_seconds} />
                </div>
              </li>
            ))}
          </ul>

          <div className="hidden overflow-x-auto sm:block">
            <table className="w-full">
              <thead>
                <tr className="border-y border-divider text-left text-xs font-medium text-slate-500">
                  <th className="px-5 py-2.5 font-medium">Time ({timezoneLabel()})</th>
                  <th className="px-3 py-2.5 font-medium">Status</th>
                  {showLatency && <th className="px-3 py-2.5 text-right font-medium">Latency</th>}
                  {showLocation && <th className="px-3 py-2.5 font-medium">Location</th>}
                  <th className="px-5 py-2.5 font-medium">Reason</th>
                </tr>
              </thead>
              <tbody>
                {rows.map(({ run, span, truncated }) => (
                  <tr key={run.latest.id} className="border-b border-divider last:border-0">
                    <td className="whitespace-nowrap px-5 py-2.5 font-mono text-xs text-slate-400">
                      <RunTime span={span} truncated={truncated} />
                      {span.duration && <span className="ml-2 font-sans text-slate-500">{span.duration}</span>}
                    </td>
                    <td className="whitespace-nowrap px-3 py-2.5">
                      <span className="inline-flex items-center gap-1.5">
                        <Pill size="xs" tone={checkResultTone(run.latest.status)}>
                          {checkResultLabel(run.latest.status)}
                        </Pill>
                        {span.count && <span className="text-xs tabular-nums text-slate-500">{span.count}</span>}
                        <SourceTag result={run.latest} />
                      </span>
                    </td>
                    {showLatency && (
                      <td
                        className="whitespace-nowrap px-3 py-2.5 text-right text-ui tabular-nums text-slate-300"
                        title={
                          run.latest.status !== 'success' && typeof run.latest.latency_ms === 'number'
                            ? `Failed after ${formatLatency(run.latest.latency_ms)}`
                            : undefined
                        }
                      >
                        {run.latest.status === 'success' ? formatLatency(run.latest.latency_ms) : <span className="text-slate-500">—</span>}
                      </td>
                    )}
                    {showLocation && (
                      <td className="whitespace-nowrap px-3 py-2.5 text-ui text-slate-300">
                        {run.latest.location_name || <span className="text-slate-500">Default</span>}
                      </td>
                    )}
                    <td className="w-full max-w-0 px-5 py-2.5 text-ui text-slate-300">
                      <Reason result={run.latest} timeoutSeconds={monitor.timeout_seconds} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}
    </section>
  );
}

// ── Overview ─────────────────────────────────────────────────────────────

export default function MonitorAnalyticsOverview({
  monitor,
  analytics,
  results,
  timeRange,
  slaTarget,
  targetSla,
  resultsLimit,
  aside,
}: {
  monitor: Monitor;
  analytics: MonitorAnalyticsResponse | null;
  results: CheckResult[];
  timeRange: MonitorAnalyticsRange;
  /** Strictest SLA target covering the monitor (lib/sla strictestTarget). */
  slaTarget: number;
  /** The SLA that sets the target, when one covers the monitor. */
  targetSla?: Sla;
  /** How many results the page fetches for Latest checks. */
  resultsLimit: number;
  /** Side column (dependencies, configuration) rendered next to the charts. */
  aside?: ReactNode;
}) {
  const summary = analytics?.summary;
  const summaryHasData = summary?.has_data ?? false;
  const headlinePct = summaryHasData ? (summary?.availability_pct ?? summary?.sla_pct ?? null) : null;
  const tone = uptimeTone(headlinePct, slaTarget);
  const showLatency = measuresLatency(monitor.type);

  const buckets: TraceBucket[] = useMemo(
    () =>
      (analytics?.uptime_series || []).map((point) => {
        const pct = point.has_data ? point.uptime_pct : null;
        return {
          ts: new Date(point.bucket_start).getTime(),
          pct,
          checks: point.total_checks,
          failed: bucketFailedChecks(point.total_checks, pct),
        };
      }),
    [analytics]
  );
  const windowStart = buckets[0]?.ts ?? null;
  const windowEnd = analytics?.generated_at ? new Date(analytics.generated_at).getTime() : Date.now();
  const windowMs = windowStart !== null ? Math.max(0, windowEnd - windowStart) : (RANGES[timeRange].ms ?? 0);

  const chartData = useMemo(
    () =>
      (analytics?.uptime_series || []).map((point, index) => ({
        ts: new Date(point.bucket_start).getTime(),
        latency: analytics?.latency_series?.[index]?.avg_latency_ms ?? null,
      })),
    [analytics]
  );
  const hasLatencyData = chartData.some((point) => typeof point.latency === 'number');

  // Newest first: the period an operator is looking for is the latest one.
  const downtimePeriods = useMemo(
    () =>
      [...(analytics?.downtime_periods ?? [])].sort(
        (a, b) => new Date(b.start_time).getTime() - new Date(a.start_time).getTime()
      ),
    [analytics]
  );
  const downtimeAreas = useMemo(
    () =>
      downtimePeriods.map((period) => ({
        id: `${period.start_time}-${period.end_time}`,
        start: Math.max(new Date(period.start_time).getTime(), windowStart ?? -Infinity),
        end: Math.min(new Date(period.end_time).getTime(), windowEnd),
      })),
    [downtimePeriods, windowStart, windowEnd]
  );
  const periodsSeconds = downtimeAreas.reduce((sum, a) => sum + Math.max(0, a.end - a.start) / 1000, 0);

  const totalChecks = buckets.reduce((sum, b) => sum + b.checks, 0);

  // Downtime in the window, derived from the headline so the two always
  // agree. Interval method: down = (1 − availability) × observed share ×
  // window, exactly the unplanned down time the timeline integrated.
  // Sampled method: downtime_pct is a failed-check ratio, not a share of the
  // window, so it applies to the time the checks covered (one interval per
  // check per location), never to the whole range: months before the monitor
  // existed or without checks would inflate it with the range.
  const sampledSpanSeconds = Math.min(
    windowMs / 1000,
    (totalChecks * monitor.interval_seconds) / Math.max(1, monitor.locations?.length ?? 0)
  );
  const downtimeSeconds =
    !summaryHasData || headlinePct === null
      ? null
      : summary?.method === 'interval'
        ? ((100 - headlinePct) / 100) * ((summary.coverage_pct ?? 100) / 100) * (windowMs / 1000)
        : ((summary?.downtime_pct ?? 100 - headlinePct) / 100) * sampledSpanSeconds;

  // The sum of the bars' tooltips, so the caption and the trace agree.
  const failedChecks = buckets.reduce((sum, b) => sum + b.failed, 0);

  const latencyIsP95 = summary?.p95_latency_ms !== undefined && summary?.p95_latency_ms !== null;
  const latencyValue = latencyIsP95 ? summary?.p95_latency_ms : summary?.avg_latency_ms;
  const lastFailed = results.find((r) => r.status !== 'success' && typeof r.latency_ms === 'number');

  const coverage = summary?.method === 'interval' ? summary.coverage_pct : undefined;
  const downtimeCaption = !summaryHasData
    ? undefined
    : summary?.method === 'interval'
      ? 'unplanned, excludes maintenance'
      : 'estimated from failed checks';
  const rangeLabel = RANGES[timeRange].long.toLowerCase();

  return (
    <div className="space-y-6">
      <StatStrip
        footer={
          buckets.length > 0 ? (
            <UptimeTrace buckets={buckets} range={timeRange} windowStart={windowStart} windowEnd={windowEnd} />
          ) : (
            <p className="py-2 text-center text-ui text-slate-500">No checks in the {rangeLabel}.</p>
          )
        }
      >
        <Stat
          label="Availability"
          value={formatUptime(headlinePct)}
          valueClassName={headlinePct === null ? 'text-slate-500' : UPTIME_TONE_TEXT[tone]}
          caption={
            <span className="flex flex-wrap gap-x-2">
              <span>target {formatTarget(slaTarget)}</span>
              {targetSla && (
                <Link
                  href={`/slas/${targetSla.id}`}
                  className="text-slate-300 underline-offset-2 transition-colors hover:text-cyan-300 hover:underline"
                >
                  SLA {targetSla.name}
                </Link>
              )}
              {summaryHasData && coverage !== undefined && coverage < 99.95 && <span>{formatUptime(coverage)} observed</span>}
            </span>
          }
        />
        <Stat
          label="Downtime"
          value={downtimeSeconds === null ? '—' : downtimeSeconds < 1 ? 'none' : formatDuration(downtimeSeconds)}
          valueClassName={downtimeSeconds !== null && downtimeSeconds >= 1 ? 'text-white' : 'text-slate-400'}
          caption={downtimeCaption}
        />
        {showLatency && (
          <Stat
            label={latencyIsP95 ? 'p95 latency' : 'Avg latency'}
            value={formatLatency(latencyValue)}
            valueClassName={latencyValue === undefined || latencyValue === null ? 'text-slate-500' : 'text-white'}
            caption={
              latencyValue === undefined || latencyValue === null
                ? lastFailed
                  ? `no successful checks; last failure took ${formatLatency(lastFailed.latency_ms)}`
                  : 'no successful checks'
                : 'successful checks'
            }
          />
        )}
        <Stat
          label="Checks"
          value={totalChecks.toLocaleString()}
          caption={
            totalChecks === 0 ? undefined : failedChecks > 0 ? (
              <span className={UPTIME_TONE_TEXT[failedChecksTone(failedChecks, totalChecks)]}>
                {failedChecks.toLocaleString()} failed
              </span>
            ) : (
              'all passed'
            )
          }
        />
      </StatStrip>

      <div className={aside ? 'grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_320px]' : undefined}>
        <div className="min-w-0 space-y-6">
          {showLatency && (
            <section className="glass p-5">
              <div className="mb-4">
                <h3 className="text-title font-semibold text-white">Response time</h3>
                <p className="mt-0.5 text-xs text-slate-500">
                  Average of successful checks per bucket. Failed checks are left out; their latency is usually the timeout.
                  {downtimeAreas.length > 0 && ' Red bands are the downtime periods listed below.'}
                </p>
              </div>
              {hasLatencyData || downtimeAreas.length > 0 ? (
                <div className="relative h-48">
                  <ResponsiveContainer width="100%" height="100%">
                    <AreaChart data={chartData} margin={{ top: 6, right: 8, left: 0, bottom: 0 }}>
                      <defs>
                        <linearGradient id="monitorLatencyGradient" x1="0" y1="0" x2="0" y2="1">
                          <stop offset="0%" stopColor={LATENCY_COLOR} stopOpacity={0.25} />
                          <stop offset="100%" stopColor={LATENCY_COLOR} stopOpacity={0} />
                        </linearGradient>
                      </defs>
                      <XAxis
                        dataKey="ts"
                        type="number"
                        domain={windowStart !== null ? [windowStart, windowEnd] : ['dataMin', 'dataMax']}
                        axisLine={false}
                        tickLine={false}
                        tick={CHART_AXIS_TICK}
                        tickMargin={8}
                        minTickGap={32}
                        tickFormatter={(value) => formatBucketLabel(value, timeRange)}
                      />
                      <YAxis
                        width={56}
                        axisLine={false}
                        tickLine={false}
                        tick={CHART_AXIS_TICK}
                        hide={!hasLatencyData}
                        domain={hasLatencyData ? [0, 'auto'] : [0, 1]}
                        tickFormatter={(value) => formatLatency(value)}
                      />
                      <Tooltip cursor={CHART_CURSOR} content={<LatencyTooltip range={timeRange} />} />
                      {downtimeAreas.map((area) => (
                        <ReferenceArea
                          key={area.id}
                          x1={area.start}
                          x2={area.end}
                          ifOverflow="hidden"
                          fill="var(--danger)"
                          fillOpacity={0.16}
                          strokeOpacity={0}
                        />
                      ))}
                      <Area
                        type="monotone"
                        dataKey="latency"
                        stroke={LATENCY_COLOR}
                        strokeWidth={2}
                        fill="url(#monitorLatencyGradient)"
                        connectNulls={false}
                        // A bucket between two empty ones has no segment to
                        // draw, so it gets a dot; the trace shows it too.
                        dot={isolatedDot(chartData, 'latency', LATENCY_COLOR)}
                        isAnimationActive={false}
                        name="Latency"
                      />
                    </AreaChart>
                  </ResponsiveContainer>
                  {!hasLatencyData && (
                    <p className="pointer-events-none absolute inset-x-0 top-1/2 -translate-y-1/2 text-center text-ui text-slate-400">
                      No successful checks in this range
                    </p>
                  )}
                </div>
              ) : (
                <p className="glass-well px-4 py-6 text-center text-sm text-slate-500">
                  No successful checks in this range. Latency is measured on successes only.
                </p>
              )}
            </section>
          )}

          <section className="glass p-5">
            <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
              <div>
                <h3 className="text-title font-semibold text-white">Downtime periods</h3>
                {/* The periods are the state timeline's down intervals
                    (the chart's red bands are drawn from this list). They
                    include down time inside maintenance windows, which the
                    Downtime stat counts as planned, so say so rather than
                    reuse the stat's "excludes maintenance" caption. */}
                <p className="mt-0.5 text-xs text-slate-500">
                  When the monitor was down, maintenance windows included. The Downtime figure above leaves maintenance out.
                </p>
              </div>
              {downtimePeriods.length > 0 && (
                <span className="flex gap-x-3 text-xs tabular-nums text-slate-500">
                  <span>{pluralize(downtimePeriods.length, 'period')}</span>
                  <span>{formatDuration(periodsSeconds)} in total</span>
                </span>
              )}
            </div>
            {downtimePeriods.length === 0 ? (
              <p className="glass-well px-4 py-6 text-center text-sm text-slate-500">
                No downtime in the {rangeLabel}.
              </p>
            ) : (
              <ul className="divide-y divide-divider">
                {downtimePeriods.slice(0, 8).map((period) => {
                  const start = formatDateTime(period.start_time);
                  const end = formatDateTime(period.end_time);
                  const sameDay = start.split(', ')[0] === end.split(', ')[0];
                  return (
                    <li
                      key={`${period.start_time}-${period.end_time}`}
                      className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2.5 text-ui tabular-nums"
                    >
                      <span className="text-slate-300" title={`${formatDateTimeFull(period.start_time)} to ${formatDateTimeFull(period.end_time)}`}>
                        {start}
                        <span className="text-slate-500"> – </span>
                        {period.is_open ? 'now' : sameDay ? end.split(', ')[1] : end}
                      </span>
                      <span className="text-slate-400">
                        {formatDuration((new Date(period.end_time).getTime() - new Date(period.start_time).getTime()) / 1000)}
                      </span>
                      {period.is_open && (
                        <Pill size="xs" tone="danger" dot>
                          Ongoing
                        </Pill>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
            {downtimePeriods.length > 8 && (
              <p className="mt-2 text-xs text-slate-500">Showing the latest 8 of {downtimePeriods.length}.</p>
            )}
          </section>

          <LatestChecks results={results} monitor={monitor} fetchLimit={resultsLimit} />
        </div>

        {aside && <div className="grid grid-cols-1 content-start gap-4 md:grid-cols-2 xl:grid-cols-1">{aside}</div>}
      </div>
    </div>
  );
}
