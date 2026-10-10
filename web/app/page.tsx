'use client';

import { Fragment, useState, useEffect, useMemo, useCallback, useRef } from 'react';
import {
  getDashboardProblemMonitors,
  getDashboardRecentAlerts,
  getDashboardRecentFailures,
  getDashboardSummary,
  getMonitorResults,
  getTenantSettings,
} from '@/lib/api';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import EmptyState from '@/components/ui/EmptyState';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import Pill from '@/components/ui/Pill';
import FilterChip from '@/components/ui/FilterChip';
import RangePicker from '@/components/ui/RangePicker';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import {
  Alert,
  CheckResult,
  DashboardFailureEvent,
  DashboardSummaryResponse,
  DashboardProblemMonitor,
} from '@/lib/types';
import Link from 'next/link';
import {
  ComposedChart,
  Area,
  CartesianGrid,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
} from 'recharts';
import { ChevronDown, Plus, RefreshCw, Tags, X } from 'lucide-react';
import {
  ActivityTimelineItem,
  OperationalSummary,
  buildActivityTimeline,
  buildOperationalSummary,
  countDegraded,
  downMonitorsHref,
  needsAttentionTotals,
  problemMonitorStatus,
  sortNeedsAttention,
} from '@/lib/dashboard-view-model';
import { formatDateTimeFull, formatLatency, formatRelativeTime, formatUptime, RANGES, rangeStart } from '@/lib/format';
import { shortFailureReason } from '@/lib/monitor-utils';
import {
  MONITOR_STATUS_LABEL,
  MONITOR_STATUS_TONE,
  UPTIME_TONE_VAR,
  uptimeTone,
} from '@/lib/status-tones';
import CheckTrace from '@/components/monitors/CheckTrace';
import ServiceGroupsPanel from '@/components/dashboard/ServiceGroupsPanel';
import { NotificationNudge } from '@/components/layout/NotificationNudge';

type TrendPoint = { date: string; uptime: number | null; responseTime: number | null; total: number };

const DASHBOARD_RANGE_OPTIONS = ['1h', '24h', '7d', '30d', '90d'] as const;
type DashboardPageRange = (typeof DASHBOARD_RANGE_OPTIONS)[number];

// The API's cap for every dashboard list. What changed keeps one row per
// monitor, so it reads the full cap of failed checks to reach enough monitors;
// the problem list is fetched whole so its states (the degraded count, group
// members) and the Needs attention total are complete.
const DASHBOARD_LIST_LIMIT = 50;

/** Rows Needs attention draws; the rest are counted in its header. */
const NEEDS_ATTENTION_ROWS = 8;

/** Checks per Needs attention trace. */
const TRACE_SLOTS = 20;

function TagFilterPicker({
  availableTags,
  selectedTags,
  onToggleTag,
  onClear,
}: {
  availableTags: string[];
  selectedTags: string[];
  onToggleTag: (tag: string) => void;
  onClear: () => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const hasSelection = selectedTags.length > 0;

  useEffect(() => {
    if (!open) return;
    function onClickOutside(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOpen(false);
    }
    document.addEventListener('mousedown', onClickOutside);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onClickOutside);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);

  if (availableTags.length === 0) return null;

  return (
    <div className="relative" ref={ref}>
      <Button
        variant="ghost"
        onClick={() => setOpen((value) => !value)}
        aria-expanded={open}
        icon={<Tags className="h-4 w-4" strokeWidth={1.75} />}
        iconTrailing={
          <ChevronDown className={`h-4 w-4 text-slate-500 transition-transform ${open ? 'rotate-180' : ''}`} strokeWidth={1.75} />
        }
        // Selected is neutral, like a selected chip: orange is for actions.
        className={hasSelection ? '!border-edge-strong !text-white' : ''}
      >
        Tags
        {hasSelection && (
          <span className="ml-1.5 rounded-full bg-well-hover px-1.5 py-px text-2xs tabular-nums text-slate-200">
            {selectedTags.length}
          </span>
        )}
      </Button>

      {open && (
        <div className="glass-overlay absolute right-0 z-50 mt-2 w-80 max-w-[calc(100vw-2rem)] !rounded-xl p-3">
          <div className="mb-3 flex h-7 items-center justify-between">
            <p className="text-xs font-medium text-slate-400">Filter dashboard by tags</p>
            {hasSelection && (
              <Button variant="subtle" size="xs" onClick={onClear}>
                Clear all
              </Button>
            )}
          </div>
          <div className="flex max-h-56 flex-wrap gap-2 overflow-y-auto pr-1">
            {availableTags.map((tag) => (
              <FilterChip
                key={tag}
                selected={selectedTags.includes(tag)}
                onClick={() => onToggleTag(tag)}
              >
                {tag}
              </FilterChip>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

// Scrolling lists fade out at the bottom edge instead of slicing a row in half.
const SCROLL_LIST =
  'dashboard-scroll divide-y divide-divider overflow-y-auto pb-4 [mask-image:linear-gradient(to_bottom,#000_calc(100%-1.5rem),transparent)]';

const ROW_LINK =
  'block px-5 py-3 transition-colors hover:bg-well focus:outline-none focus-visible:bg-well focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-cyan-500/50';

// ─── Failure reason ────────────────────────────────────────────────────────────

// The short cause ("connection refused", "HTTP 503"); the raw worker message
// stays one hover away in the title.
function ReasonText({ message, className = '' }: { message: string | null | undefined; className?: string }) {
  const short = shortFailureReason(message);
  if (!short) return null;
  return (
    <span className={`min-w-0 truncate text-slate-400 ${className}`} title={message ?? undefined}>
      {short}
    </span>
  );
}

// ─── Problem Monitor Item ──────────────────────────────────────────────────────

function ProblemMonitorItem({
  monitor,
  results,
  rangeLabel,
}: {
  monitor: DashboardProblemMonitor;
  results: CheckResult[] | null;
  rangeLabel: string;
}) {
  const issueCount = monitor.failure_count + monitor.error_count;
  // The monitor's current state, never inferred from its latest check: a
  // monitor failing from one location is degraded whatever its last check did.
  const status = problemMonitorStatus(monitor);

  return (
    <Link href={`/monitors/${monitor.monitor_id}`} className={ROW_LINK}>
      <div className="flex min-w-0 items-center justify-between gap-3">
        <span className="min-w-0 truncate text-sm font-medium text-white">{monitor.monitor_name}</span>
        <Pill tone={MONITOR_STATUS_TONE[status]} size="xs" dot className="shrink-0">
          {MONITOR_STATUS_LABEL[status]}
        </Pill>
      </div>
      <div className="mt-2 flex items-center gap-3">
        <CheckTrace results={results} slots={TRACE_SLOTS} className="min-w-0 flex-1" />
        <span
          className="w-12 shrink-0 text-right text-ui font-medium tabular-nums text-slate-300"
          title={`Uptime, ${rangeLabel.toLowerCase()}`}
        >
          {formatUptime(monitor.uptime)}
        </span>
      </div>
      <div className="mt-1.5 flex min-w-0 items-center justify-between gap-3 text-xs">
        <ReasonText message={monitor.latest_error_message} />
        <span className="ml-auto flex shrink-0 gap-2 tabular-nums text-slate-500">
          <span>{issueCount} failed</span>
          {monitor.latest_failure_at && (
            <span title={formatDateTimeFull(monitor.latest_failure_at)}>
              {formatRelativeTime(monitor.latest_failure_at)}
            </span>
          )}
        </span>
      </div>
    </Link>
  );
}

// ─── Chart Tooltip ─────────────────────────────────────────────────────────────

const TOOLTIP_STYLE: React.CSSProperties = {
  background: 'var(--glass-fill-strong)',
  border: '1px solid var(--glass-edge-strong)',
  borderRadius: 12,
  backdropFilter: 'blur(20px)',
  WebkitBackdropFilter: 'blur(20px)',
  color: 'var(--text-primary)',
};

const AXIS_TICK = { fill: 'var(--text-muted)', fontSize: 11 };

const UPTIME_TICK_STEPS = [0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 25];

const LATENCY_COLOR = '#6fb5dd';

function CustomTooltip({ active, payload, label, uptimeColor }: any) {
  if (!active || !payload?.length) return null;
  return (
    <div className="px-3.5 py-2.5" style={TOOLTIP_STYLE}>
      <p className="mb-1.5 text-xs font-medium text-slate-400">{label}</p>
      <div className="space-y-1">
        {payload.map((entry: any, idx: number) => {
          const isLatency = entry.name === 'responseTime';
          return (
            <div key={idx} className="flex items-center justify-between gap-4">
              <span className="flex items-center gap-1.5 text-xs text-slate-400">
                <span className="h-1.5 w-1.5 rounded-full" style={{ background: isLatency ? LATENCY_COLOR : uptimeColor }} />
                {isLatency ? 'Avg HTTP latency' : 'Uptime'}
              </span>
              <span className="text-sm font-semibold tabular-nums text-white">
                {typeof entry.value === 'number'
                  ? isLatency
                    ? formatLatency(entry.value)
                    : formatUptime(entry.value)
                  : entry.value}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ─── Loading Skeleton ──────────────────────────────────────────────────────────

function LoadingSkeleton() {
  return (
    <div className="animate-pulse space-y-6" role="status" aria-label="Loading dashboard">
      <div className="flex items-center justify-between">
        <div className="h-8 w-40 rounded-lg bg-well-hover" />
        <div className="hidden h-9 w-72 rounded-lg bg-well-hover sm:block" />
      </div>
      <div className="glass h-36" />
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.8fr)]">
        <div className="glass h-[22rem]" />
        <div className="glass h-[22rem]" />
      </div>
    </div>
  );
}

// ─── Section Card ──────────────────────────────────────────────────────────────

function SectionCard({
  title,
  action,
  children,
  className,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <section className={`glass flex min-w-0 flex-col overflow-hidden ${className ?? ''}`}>
      <div className="flex min-w-0 shrink-0 flex-wrap items-center justify-between gap-2 px-5 py-4">
        <h3 className="min-w-0 text-title font-semibold text-white">{title}</h3>
        {action && <div className="min-w-0 text-ui text-slate-500">{action}</div>}
      </div>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">{children}</div>
    </section>
  );
}

const SUMMARY_DOT: Record<OperationalSummary['tone'], string> = {
  critical: 'bg-rose-500',
  attention: 'bg-amber-500',
  stable: 'bg-emerald-500',
  clean: 'bg-emerald-500',
};

// Wraps between facts, never inside one ("3 / degraded").
function HeadlineFacts({ label }: { label: string }) {
  const facts = label.split(', ');
  return (
    <>
      {facts.map((fact, i) => (
        <Fragment key={i}>
          {i > 0 && ' '}
          <span className="whitespace-nowrap">
            {fact}
            {i < facts.length - 1 && ','}
          </span>
        </Fragment>
      ))}
    </>
  );
}

function OperationalSummaryCard({ summary }: { summary: OperationalSummary }) {
  const action = summary.primaryAction;

  return (
    <section className="glass grid gap-4 p-4 sm:gap-6 sm:p-5 lg:grid-cols-[minmax(0,0.8fr)_minmax(0,2fr)] lg:items-center">
      <div className="min-w-0">
        <div className="flex items-start gap-2.5">
          <span className={`mt-2.5 h-2 w-2 shrink-0 rounded-full ${SUMMARY_DOT[summary.tone]}`} aria-hidden="true" />
          {/* On mobile the headline is the link; the button joins it from sm. */}
          <h2 className="text-balance text-lg font-semibold leading-7 text-white">
            {action ? (
              // Below sm the button is hidden and this is the only way in, so
              // it wears the link colour and underline that touch can't hover for.
              <Link
                href={action.href}
                className="text-cyan-300 underline decoration-cyan-500/40 underline-offset-4 transition-colors sm:text-white sm:no-underline sm:hover:text-cyan-300 sm:hover:underline"
              >
                <HeadlineFacts label={summary.label} />
              </Link>
            ) : (
              <HeadlineFacts label={summary.label} />
            )}
          </h2>
        </div>
        {summary.description && (
          <p className="mt-1 hidden max-w-xl pl-[1.125rem] text-sm text-slate-400 sm:block">{summary.description}</p>
        )}
        {action && (
          <div className="mt-4 hidden pl-[1.125rem] sm:block">
            <Button variant="ghost" asChild>
              <Link href={action.href}>{action.label}</Link>
            </Button>
          </div>
        )}
      </div>

      <div className="grid grid-cols-2 gap-3 xl:grid-cols-4">
        {summary.metrics.map((metric) => {
          // Healthy values stay neutral so only a problem carries colour.
          const valueTone =
            metric.tone === 'critical'
              ? 'text-rose-300'
              : metric.tone === 'attention'
                ? 'text-amber-300'
                : 'text-white';
          return (
            <div key={metric.label} className="glass-well min-w-0 p-3 sm:p-4" title={metric.title}>
              <p className="truncate text-xs text-slate-400">{metric.label}</p>
              <p className={`mt-1 text-xl font-semibold tabular-nums sm:text-2xl ${valueTone}`}>{metric.value}</p>
              {metric.detail && <p className="mt-1 hidden truncate text-xs text-slate-500 sm:block">{metric.detail}</p>}
            </div>
          );
        })}
      </div>
    </section>
  );
}

function UptimeResponseChart({
  trendData,
  hasEnoughTrendData,
  noMatchingMonitors,
}: {
  trendData: TrendPoint[];
  hasEnoughTrendData: boolean;
  noMatchingMonitors: boolean;
}) {
  const { uptimeAxis, uptimeColor } = useMemo(() => {
    const values = trendData
      .map((point) => point.uptime)
      .filter((value): value is number => typeof value === 'number');
    const min = values.length === 0 ? 98.5 : Math.min(...values);
    const padded = Math.max(0, min - Math.max(0.25, (100 - min) * 0.08));
    // Evenly spaced ticks ending at 100%, so the axis never skips a label.
    const step = UPTIME_TICK_STEPS.find((s) => (100 - padded) / s <= 4) ?? 25;
    const count = Math.max(1, Math.ceil((100 - padded) / step - 1e-9));
    const ticks = Array.from({ length: count + 1 }, (_, i) => Math.round((100 - step * (count - i)) * 10) / 10);
    // The line is neutral ink while uptime is healthy and takes the shared
    // tone of its worst bucket otherwise, so green never draws an outage.
    const tone = uptimeTone(values.length === 0 ? null : min);
    return {
      uptimeAxis: { domain: [ticks[0], 100] as [number, number], ticks },
      uptimeColor: tone === 'warn' || tone === 'bad' ? UPTIME_TONE_VAR[tone] : 'var(--text-secondary)',
    };
  }, [trendData]);

  return (
    <section className="glass min-w-0 overflow-hidden">
      <div className="px-5 py-4">
        <h3 className="text-title font-semibold text-white">Uptime and latency</h3>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-slate-400">
          <span className="flex items-center gap-1.5">
            <span className="h-2 w-2 rounded-full" style={{ background: uptimeColor }} /> Uptime
          </span>
          <span className="flex items-center gap-1.5">
            <span className="h-2 w-2 rounded-full" style={{ background: LATENCY_COLOR }} /> Avg HTTP latency
          </span>
        </div>
      </div>

      <div className="px-5 pb-5">
        {hasEnoughTrendData ? (
          <div className="h-64 min-w-0">
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={trendData} margin={{ top: 10, right: 6, bottom: 0, left: 4 }}>
                <defs>
                  <linearGradient id="combinedUptimeGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={uptimeColor} stopOpacity={0.18} />
                    <stop offset="100%" stopColor={uptimeColor} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke="var(--divider)" vertical={false} />
                <XAxis dataKey="date" axisLine={false} tickLine={false} tick={AXIS_TICK} minTickGap={24} />
                <YAxis
                  yAxisId="uptime"
                  domain={uptimeAxis.domain}
                  ticks={uptimeAxis.ticks}
                  interval={0}
                  width={58}
                  axisLine={false}
                  tickLine={false}
                  tick={AXIS_TICK}
                  tickFormatter={(v) => `${Number(v).toFixed(1).replace(/\.0$/, '')}%`}
                />
                <YAxis
                  yAxisId="latency"
                  orientation="right"
                  // Room for "200 ms" on one line.
                  width={60}
                  axisLine={false}
                  tickLine={false}
                  tick={AXIS_TICK}
                  tickFormatter={(v) => formatLatency(Number(v))}
                />
                <Tooltip
                  content={<CustomTooltip uptimeColor={uptimeColor} />}
                  cursor={{ stroke: 'var(--glass-edge-strong)', strokeDasharray: '3 3' }}
                />
                <Area
                  yAxisId="uptime"
                  type="monotone"
                  dataKey="uptime"
                  stroke={uptimeColor}
                  strokeWidth={2}
                  fill="url(#combinedUptimeGradient)"
                  name="uptime"
                  // Drawn at once: a redraw animation on every refresh adds
                  // nothing on an ops dashboard and ignores reduced motion.
                  isAnimationActive={false}
                  activeDot={{ r: 3.5, fill: uptimeColor, stroke: 'var(--bg-elevated)', strokeWidth: 2 }}
                />
                <Line
                  yAxisId="latency"
                  type="monotone"
                  dataKey="responseTime"
                  stroke={LATENCY_COLOR}
                  strokeWidth={2}
                  dot={false}
                  name="responseTime"
                  isAnimationActive={false}
                  activeDot={{ r: 3.5, fill: LATENCY_COLOR, stroke: 'var(--bg-elevated)', strokeWidth: 2 }}
                />
              </ComposedChart>
            </ResponsiveContainer>
          </div>
        ) : (
          <div className="glass-well flex h-64 items-center justify-center px-4 text-center">
            <div>
              <p className="text-sm font-medium text-slate-300">
                {noMatchingMonitors ? 'No monitors match these tags' : 'Not enough trend data yet'}
              </p>
              <p className="mt-1 text-ui text-slate-500">
                {noMatchingMonitors ? 'Choose fewer tags to widen the dashboard scope.' : 'Trend lines will appear after at least two populated buckets.'}
              </p>
            </div>
          </div>
        )}
      </div>
    </section>
  );
}

// A section whose data failed to load says so; an empty state here would
// claim an all-clear the page never checked.
function SectionLoadError({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="flex items-center justify-between gap-3 px-5 py-4 text-sm text-slate-400">
      <span>Couldn&apos;t load this section.</span>
      <Button variant="ghost" size="xs" onClick={onRetry}>
        Retry
      </Button>
    </div>
  );
}

function NeedsAttentionPanel({
  monitors,
  traces,
  total,
  unhealthyCount,
  downHref,
  loading,
  loadFailed,
  onRetry,
  noMatchingMonitors,
  rangeLabel,
}: {
  monitors: DashboardProblemMonitor[];
  traces: Record<string, CheckResult[]>;
  /** Every monitor needing attention; the panel draws the first NEEDS_ATTENTION_ROWS. */
  total: number;
  /** Monitors down or degraded now. */
  unhealthyCount: number;
  /** The down-monitors list (it includes degraded) in the dashboard's tag scope. */
  downHref: string;
  loading: boolean;
  /** The list never loaded: show the failure, not "Nothing needs attention". */
  loadFailed: boolean;
  onRetry: () => void;
  noMatchingMonitors: boolean;
  rangeLabel: string;
}) {
  const shown = monitors.length;
  const unhealthyShown = monitors.filter((m) => {
    const status = problemMonitorStatus(m);
    return status === 'down' || status === 'degraded';
  }).length;
  return (
    <SectionCard
      title="Needs attention"
      action={
        shown > 0 ? (
          <Pill tone="neutral" size="xs" className="tabular-nums">
            {total > shown ? `${shown} of ${total}` : shown}
          </Pill>
        ) : undefined
      }
    >
      {loadFailed ? (
        <SectionLoadError onRetry={onRetry} />
      ) : loading ? (
        <TableSkeleton rows={3} columns={1} bare label="Loading monitors needing attention" />
      ) : shown > 0 ? (
        <>
          <div className="divide-y divide-divider">
            {monitors.map((monitor) => (
              <ProblemMonitorItem
                key={monitor.monitor_id}
                monitor={monitor}
                results={traces[monitor.monitor_id] ?? null}
                rangeLabel={rangeLabel}
              />
            ))}
          </div>
          {unhealthyCount > unhealthyShown && (
            <Link
              href={downHref}
              className="border-t border-divider px-5 py-3 text-sm text-cyan-300 transition-colors hover:bg-well hover:text-cyan-200 focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-cyan-500/50"
            >
              View all {unhealthyCount} down or degraded
            </Link>
          )}
        </>
      ) : (
        <EmptyState
          variant="inline"
          title={noMatchingMonitors ? 'No monitors match these tags' : 'Nothing needs attention'}
          description={
            noMatchingMonitors
              ? 'Choose fewer tags to widen the dashboard scope.'
              : `No failed checks in the ${rangeLabel.toLowerCase()}.`
          }
        />
      )}
    </SectionCard>
  );
}

function TimelineRow({ item }: { item: ActivityTimelineItem }) {
  const body = (
    <>
      <div className="flex min-w-0 items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <span className="min-w-0 truncate text-sm font-medium text-white">{item.title}</span>
          <Pill tone={item.tone} size="xs" className="shrink-0">
            {item.event}
          </Pill>
        </div>
        <span className="shrink-0 text-xs tabular-nums text-slate-500" title={formatDateTimeFull(item.occurredAt)}>
          {item.relativeTime}
        </span>
      </div>
      {(item.reason || item.detail) && (
        <div className="mt-1 flex min-w-0 items-center justify-between gap-3 text-xs">
          <ReasonText message={item.reason} />
          {item.detail && <span className="ml-auto shrink-0 tabular-nums text-slate-500">{item.detail}</span>}
        </div>
      )}
    </>
  );
  return item.href ? (
    <Link href={item.href} className={ROW_LINK}>{body}</Link>
  ) : (
    <div className="px-5 py-3">{body}</div>
  );
}

function WhatChangedTimeline({
  items,
  loading,
  loadFailed,
  onRetry,
  rangeLabel,
}: {
  items: ActivityTimelineItem[];
  loading: boolean;
  /** The activity never loaded: show the failure, not "No changes". */
  loadFailed: boolean;
  onRetry: () => void;
  rangeLabel: string;
}) {
  return (
    <SectionCard
      title="What changed"
      action={
        <Link href="/alerts" className="text-cyan-300 transition-colors hover:text-cyan-200 hover:underline">
          Alert history
        </Link>
      }
    >
      {loadFailed ? (
        <SectionLoadError onRetry={onRetry} />
      ) : loading ? (
        <TableSkeleton rows={3} columns={1} bare label="Loading recent activity" />
      ) : items.length > 0 ? (
        <div className={`${SCROLL_LIST} max-h-[28rem]`}>
          {items.map((item) => (
            <TimelineRow key={item.id} item={item} />
          ))}
        </div>
      ) : (
        <EmptyState
          variant="inline"
          title="No changes"
          description={`Failing monitors and alerts in the ${rangeLabel.toLowerCase()} appear here.`}
        />
      )}
    </SectionCard>
  );
}

// ─── Dashboard Page ────────────────────────────────────────────────────────────

export default function DashboardPage() {
  const canWrite = useCanWrite();
  const [dashboard, setDashboard] = useState<DashboardSummaryResponse | null>(null);
  const [problemMonitorsData, setProblemMonitorsData] = useState<DashboardProblemMonitor[] | null>(null);
  const [recentFailuresData, setRecentFailuresData] = useState<DashboardFailureEvent[] | null>(null);
  const [recentAlertsData, setRecentAlertsData] = useState<Alert[] | null>(null);
  const [traces, setTraces] = useState<Record<string, CheckResult[]>>({});
  const [tenantRetentionDays, setTenantRetentionDays] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  // Set when the panels' data failed to load; a section that has no data then
  // renders the failure instead of an empty (all-clear) state.
  const [sectionsError, setSectionsError] = useState(false);
  const [timeRange, setTimeRange] = useState<DashboardPageRange>('1h');
  const [selectedTags, setSelectedTags] = useState<string[]>([]);
  const requestSequenceRef = useRef(0);

  const loadSecondarySections = useCallback(async (
    requestID: number,
    range: DashboardPageRange,
    tags: string[]
  ) => {
    try {
      const [problemResponse, failuresResponse, alertsResponse] = await Promise.all([
        getDashboardProblemMonitors({ range, limit: DASHBOARD_LIST_LIMIT, tags }),
        getDashboardRecentFailures({ range, limit: DASHBOARD_LIST_LIMIT, tags }),
        getDashboardRecentAlerts({ range, limit: DASHBOARD_LIST_LIMIT, tags }),
      ]);

      if (requestSequenceRef.current !== requestID) {
        return;
      }

      setProblemMonitorsData(problemResponse.problem_monitors);
      setRecentFailuresData(failuresResponse.recent_failures);
      setRecentAlertsData(alertsResponse.recent_alerts);
      setSectionsError(false);
    } catch (err) {
      console.error('Failed to load dashboard secondary sections:', err);
      if (requestSequenceRef.current !== requestID) {
        return;
      }
      // Keep whatever was on screen; sections with nothing show the failure.
      setSectionsError(true);
    }
  }, []);

  // A refetch (range, tags, refresh) keeps the current sections on screen
  // until the new data lands; only the first load shows skeletons.
  const loadData = useCallback(async () => {
    const requestID = requestSequenceRef.current + 1;
    requestSequenceRef.current = requestID;

    try {
      setLoading(true);
      setError(null);
      setSectionsError(false);

      const [response, settings] = await Promise.all([
        getDashboardSummary({
          range: timeRange,
          tags: selectedTags,
        }),
        getTenantSettings().catch(() => null),
      ]);

      if (requestSequenceRef.current !== requestID) {
        return;
      }

      setDashboard(response);
      if (settings) setTenantRetentionDays(settings.data_retention_days);

      if (typeof window !== 'undefined') {
        window.requestAnimationFrame(() => {
          void loadSecondarySections(requestID, timeRange, selectedTags);
        });
      } else {
        void loadSecondarySections(requestID, timeRange, selectedTags);
      }
    } catch (err) {
      console.error('Failed to load dashboard data:', err);
      if (requestSequenceRef.current === requestID) {
        setError('Failed to load dashboard data');
        setSectionsError(true);
      }
    } finally {
      if (requestSequenceRef.current === requestID) {
        setLoading(false);
      }
    }
  }, [loadSecondarySections, selectedTags, timeRange]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const problemMonitors = useMemo(() => problemMonitorsData || [], [problemMonitorsData]);
  const sortedProblems = useMemo(() => sortNeedsAttention(problemMonitors), [problemMonitors]);
  const needsAttention = useMemo(() => sortedProblems.slice(0, NEEDS_ATTENTION_ROWS), [sortedProblems]);

  // Each Needs attention row draws its recent checks. Fetched per drawn row
  // (at most NEEDS_ATTENTION_ROWS) whenever the list reloads.
  useEffect(() => {
    if (needsAttention.length === 0) return;
    let cancelled = false;
    void Promise.all(
      needsAttention.map(async (monitor) => {
        try {
          const response = await getMonitorResults(monitor.monitor_id, { limit: TRACE_SLOTS });
          return [monitor.monitor_id, response.results] as const;
        } catch {
          return [monitor.monitor_id, [] as CheckResult[]] as const;
        }
      }),
    ).then((entries) => {
      if (cancelled) return;
      setTraces((current) => ({ ...current, ...Object.fromEntries(entries) }));
    });
    return () => {
      cancelled = true;
    };
  }, [needsAttention]);

  const availableTags = useMemo(() => dashboard?.available_tags || [], [dashboard]);

  useEffect(() => {
    if (availableTags.length === 0) {
      setSelectedTags((current) => (current.length === 0 ? current : []));
      return;
    }
    setSelectedTags((current) => {
      const next = current.filter((tag) => availableTags.includes(tag));
      return next.length === current.length ? current : next;
    });
  }, [availableTags]);

  const toggleTag = useCallback((tag: string) => {
    setSelectedTags((current) =>
      current.includes(tag)
        ? current.filter((value) => value !== tag)
        : [...current, tag].sort((a, b) => a.localeCompare(b))
    );
  }, []);

  const clearTags = useCallback(() => {
    setSelectedTags((current) => (current.length === 0 ? current : []));
  }, []);

  const trendData = useMemo<TrendPoint[]>(() => {
    return (dashboard?.trend || []).map((point) => ({
      date: point.label,
      uptime: point.total_checks > 0 ? point.uptime : null,
      responseTime: point.total_checks > 0 ? point.response_time : null,
      total: point.total_checks,
    }));
  }, [dashboard]);

  const populatedTrendPoints = useMemo(() => trendData.filter((d) => d.total > 0).length, [trendData]);
  const hasEnoughTrendData = populatedTrendPoints >= 2;

  const opsSummary = dashboard?.ops_summary;
  const degradedMonitors = useMemo(() => countDegraded(problemMonitorsData), [problemMonitorsData]);
  const recentFailures = useMemo(() => recentFailuresData || [], [recentFailuresData]);
  const recentAlerts = useMemo(() => recentAlertsData || [], [recentAlertsData]);

  const rangeLabel = RANGES[timeRange].long;
  const stats = dashboard?.stats;
  const totalMonitors = stats?.total_monitors || 0;
  const hasTagFilter = selectedTags.length > 0;
  const noMatchingMonitors = hasTagFilter && totalMonitors === 0;
  const operationalSummary = useMemo(
    () => buildOperationalSummary({
      opsSummary,
      problemMonitorsCount: problemMonitors.length,
      degradedMonitors,
      recentFailuresCount: recentFailures.length,
      overallUptime: stats?.overall_uptime ?? null,
      avgResponseMs: stats?.avg_response_ms || 0,
      totalMonitors,
      httpMonitors: stats?.http_monitors,
      rangeLabel,
      tags: selectedTags,
    }),
    [opsSummary, problemMonitors.length, degradedMonitors, recentFailures.length, stats?.avg_response_ms, stats?.overall_uptime, stats?.http_monitors, totalMonitors, rangeLabel, selectedTags],
  );
  const attentionTotals = useMemo(
    () => needsAttentionTotals(problemMonitorsData, DASHBOARD_LIST_LIMIT, {
      total: operationalSummary.attentionCount,
      unhealthy: (opsSummary?.down_monitors ?? 0) + (degradedMonitors ?? 0),
    }),
    [problemMonitorsData, operationalSummary.attentionCount, opsSummary?.down_monitors, degradedMonitors],
  );
  const downHref = useMemo(() => downMonitorsHref(selectedTags), [selectedTags]);
  const timelineItems = useMemo(
    () => buildActivityTimeline({
      failures: recentFailures,
      alerts: recentAlerts,
      monitors: problemMonitors,
      since: rangeStart(timeRange),
    }).slice(0, 8),
    [problemMonitors, recentAlerts, recentFailures, timeRange],
  );

  const selectedRangeDays = Math.floor((RANGES[timeRange].ms ?? 0) / 86_400_000);
  const showRetentionWarning =
    tenantRetentionDays !== null && tenantRetentionDays > 0 && selectedRangeDays > tenantRetentionDays;

  if (loading && dashboard === null && !error) return <LoadingSkeleton />;

  return (
    <div className="space-y-6">
      <NotificationNudge unroutedMonitors={opsSummary?.unrouted_monitors ?? 0} />

      {error && (
        <Callout
          tone="danger"
          action={<Button variant="danger" size="xs" onClick={loadData}>Retry</Button>}
        >
          {error}
        </Callout>
      )}

      <PageHeader
        title="Dashboard"
        action={
          <>
            {/* The range scopes every panel on the page, so it lives here. */}
            <RangePicker ranges={DASHBOARD_RANGE_OPTIONS} value={timeRange} onChange={setTimeRange} />
            <TagFilterPicker
              availableTags={availableTags}
              selectedTags={selectedTags}
              onToggleTag={toggleTag}
              onClear={clearTags}
            />
            <IconButton
              icon={<RefreshCw strokeWidth={1.75} />}
              label="Refresh dashboard"
              onClick={loadData}
              loading={loading}
            />
            {canWrite && (
              <Button variant="accent" icon={<Plus className="h-4 w-4" strokeWidth={1.75} />} asChild>
                <Link href="/monitors/new">Add monitor</Link>
              </Button>
            )}
          </>
        }
      />

      {hasTagFilter && (
        <div className="flex flex-wrap items-center gap-2 text-ui">
          <span className="text-slate-400">Scoped to</span>
          {selectedTags.map((tag) => (
            <Pill key={tag} tone="neutral">
              {tag}
            </Pill>
          ))}
          <Button variant="subtle" size="xs" icon={<X className="h-3.5 w-3.5" strokeWidth={1.75} />} onClick={clearTags}>
            Clear
          </Button>
        </div>
      )}

      {showRetentionWarning && (
        <Callout tone="warning">
          Data retention is set to {tenantRetentionDays} day{tenantRetentionDays === 1 ? '' : 's'}.
          Older history is deleted, so this {timeRange} view may be partial.
        </Callout>
      )}

      {noMatchingMonitors && (
        <Callout tone="info" title="No monitors match the selected tag combination.">
          Adjust the tag filter to broaden the dashboard scope.
        </Callout>
      )}

      {dashboard && (
        <div className="dash-enter">
          <OperationalSummaryCard summary={operationalSummary} />
        </div>
      )}

      {/* Two independent columns at xl (chart + changes | attention + groups),
          so a short card never leaves a hole beside a tall one. Below xl the
          column wrappers dissolve (`contents`) and `order` puts the problem
          list first. */}
      {/* Without a summary there is nothing true to draw: the error callout
          above carries Retry, and empty panels would read as an all-clear. */}
      {dashboard && (
      <div className="dash-enter flex flex-col gap-6 xl:grid xl:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.8fr)] xl:items-start">
        <div className="contents xl:flex xl:min-w-0 xl:flex-col xl:gap-6">
          <div className="order-2 min-w-0 xl:order-none">
            <UptimeResponseChart
              trendData={trendData}
              hasEnoughTrendData={hasEnoughTrendData}
              noMatchingMonitors={noMatchingMonitors}
            />
          </div>
          <div className="order-3 min-w-0 xl:order-none">
            <WhatChangedTimeline
              items={timelineItems}
              loading={recentFailuresData === null || recentAlertsData === null}
              loadFailed={sectionsError && (recentFailuresData === null || recentAlertsData === null)}
              onRetry={loadData}
              rangeLabel={rangeLabel}
            />
          </div>
        </div>
        <div className="contents xl:flex xl:min-w-0 xl:flex-col xl:gap-6">
          <div className="order-1 min-w-0 xl:order-none">
            <NeedsAttentionPanel
              monitors={needsAttention}
              traces={traces}
              total={attentionTotals.total}
              unhealthyCount={attentionTotals.unhealthy}
              downHref={downHref}
              loading={problemMonitorsData === null}
              loadFailed={sectionsError && problemMonitorsData === null}
              onRetry={loadData}
              noMatchingMonitors={noMatchingMonitors}
              rangeLabel={rangeLabel}
            />
          </div>
          <div className="order-4 min-w-0 xl:order-none">
            <ServiceGroupsPanel
              groupTags={dashboard.group_tags ?? []}
              groups={dashboard.groups ?? []}
              range={timeRange}
              filterTags={selectedTags}
              monitorStates={problemMonitors}
            />
          </div>
        </div>
      </div>
      )}
    </div>
  );
}
