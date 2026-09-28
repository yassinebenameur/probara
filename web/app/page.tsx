'use client';

import { useState, useEffect, useMemo, useCallback, useRef } from 'react';
import {
  getDashboardProblemMonitors,
  getDashboardRecentAlerts,
  getDashboardRecentFailures,
  getDashboardSummary,
  getTenantSettings,
} from '@/lib/api';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import PageHeader from '@/components/ui/PageHeader';
import Pill, { PillTone } from '@/components/ui/Pill';
import FilterChip from '@/components/ui/FilterChip';
import InfoTip, { InfoTipEntry } from '@/components/ui/InfoTip';
import {
  Alert,
  DashboardRange,
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
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  Check,
  ChevronDown,
  Clock3,
  Plus,
  RefreshCw,
  Tags,
  X,
} from 'lucide-react';
import {
  ActivityTimelineItem,
  FailureReasonCategory,
  OperationalSummary,
  buildActivityTimeline,
  buildOperationalSummary,
  parseFailureReason,
  sortNeedsAttention,
} from '@/lib/dashboard-view-model';
import ServiceGroupsPanel from '@/components/dashboard/ServiceGroupsPanel';
import { NotificationNudge } from '@/components/layout/NotificationNudge';

type TrendPoint = { date: string; uptime: number | null; responseTime: number | null; total: number };

const DASHBOARD_RANGE_OPTIONS = ['1h', '24h', '7d', '30d', '90d'] as const;
type DashboardPageRange = (typeof DASHBOARD_RANGE_OPTIONS)[number];

const DASHBOARD_RANGE_LABELS: Record<DashboardPageRange, string> = {
  '1h': 'Last hour',
  '24h': 'Last 24 hours',
  '7d': 'Last 7 days',
  '30d': 'Last 30 days',
  '90d': 'Last 90 days',
};

const DASHBOARD_LIST_LIMIT: Record<DashboardPageRange, number> = {
  '1h': 10,
  '24h': 10,
  '7d': 25,
  '30d': 50,
  '90d': 50,
};

function formatRelativeTime(dateString: string): string {
  const date = new Date(dateString);
  const now = new Date();
  const seconds = Math.floor((now.getTime() - date.getTime()) / 1000);
  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

// ─── Info Popover ──────────────────────────────────────────────────────────────
// Now provided by <InfoTip> from components/ui/InfoTip.

type PopoverEntry = InfoTipEntry;

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
        icon={<Tags className="h-4 w-4" strokeWidth={1.75} />}
        iconTrailing={
          <ChevronDown className={`h-4 w-4 text-slate-500 transition-transform ${open ? 'rotate-180' : ''}`} strokeWidth={1.75} />
        }
        className={hasSelection ? '!border-cyan-500/40 !text-cyan-300' : ''}
      >
        Tags
        {hasSelection && (
          <span className="ml-1.5 rounded-full bg-cyan-500/[0.14] px-1.5 py-px text-[11px] tabular-nums text-cyan-300">
            {selectedTags.length}
          </span>
        )}
      </Button>

      {open && (
        <div className="glass-overlay absolute right-0 z-50 mt-2 w-80 !rounded-xl p-3">
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

function SectionLoadingState({ message }: { message: string }) {
  return (
    <div className="flex min-h-40 flex-1 items-center justify-center gap-2 px-5 pb-5 text-sm text-slate-500">
      <RefreshCw className="h-4 w-4 animate-spin" strokeWidth={1.75} />
      <span>{message}</span>
    </div>
  );
}

// ─── Failure Reason Line ───────────────────────────────────────────────────────

const REASON_CATEGORY_META: Record<FailureReasonCategory, { label: string; tone: PillTone }> = {
  timeout: { label: 'Timeout', tone: 'warning' },
  latency: { label: 'Latency', tone: 'warning' },
  dns: { label: 'DNS', tone: 'danger' },
  connect: { label: 'Connect', tone: 'danger' },
  status: { label: 'Status', tone: 'warning' },
  tls: { label: 'TLS', tone: 'warning' },
};

// One truncated line: optional category pill + the message, minus the prefix
// the pill already names. The full untruncated message lives in the row's
// InfoTip popover and the line's title.
function FailureReasonLine({ message }: { message: string | null | undefined }) {
  const reason = parseFailureReason(message);
  if (!reason) return null;
  const meta = reason.category ? REASON_CATEGORY_META[reason.category] : null;
  const shown = meta ? reason.text.slice(reason.text.indexOf(':') + 1).trim() || reason.text : reason.text;
  return (
    <div className="mt-1 flex min-w-0 items-center gap-1.5">
      {meta && (
        <Pill tone={meta.tone} size="xs" className="shrink-0">
          {meta.label}
        </Pill>
      )}
      <span className="min-w-0 truncate text-xs text-slate-400" title={reason.text}>
        {shown}
      </span>
    </div>
  );
}

// ─── Problem Monitor Item ──────────────────────────────────────────────────────

function ProblemMonitorItem({ monitor }: { monitor: DashboardProblemMonitor }) {
  const issueCount = monitor.failure_count + monitor.error_count;
  const uptimePct = Math.max(0, Math.min(100, monitor.uptime));
  const uptimeGood = uptimePct >= 99;
  const uptimeMid = uptimePct >= 95;
  const barColor = uptimeGood
    ? 'bg-emerald-500'
    : uptimeMid
      ? 'bg-amber-500'
      : 'bg-rose-500';
  const uptimeTextColor = uptimeGood
    ? 'text-slate-200'
    : uptimeMid
      ? 'text-amber-300'
      : 'text-rose-300';
  const barWidth = uptimePct > 0 ? Math.max(uptimePct, 2) : 0;

  const infoEntries: PopoverEntry[] = [
    { label: 'Failures', value: monitor.failure_count },
    { label: 'Errors', value: monitor.error_count },
    { label: 'Total issues', value: issueCount },
    { label: 'Latest issue', value: monitor.latest_failure_at ? formatRelativeTime(monitor.latest_failure_at) : '—' },
    { label: 'Uptime', value: `${uptimePct.toFixed(2)}%` },
  ];
  if (monitor.latest_error_message) {
    infoEntries.push({
      label: 'Reason',
      value: <span className="break-words">{monitor.latest_error_message}</span>,
    });
  }

  return (
    <div className="px-5 py-3.5 transition-colors hover:bg-well">
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 items-start gap-2">
          <Link
            href={`/monitors/${monitor.monitor_id}`}
            className="min-w-0 whitespace-normal break-all text-sm font-medium text-white transition-colors hover:text-cyan-300"
          >
            {monitor.monitor_name}
          </Link>
          <InfoTip entries={infoEntries} title={monitor.monitor_name} />
        </div>
        <span className={`shrink-0 text-[13px] font-medium tabular-nums ${uptimeTextColor}`}>
          {uptimePct.toFixed(1)}%
        </span>
      </div>
      <div className="mt-2 h-1 overflow-hidden rounded-full bg-well-hover">
        <div
          className={`h-full rounded-full ${barColor} transition-all`}
          style={{ width: `${barWidth}%` }}
        />
      </div>
      <p className="mt-2 text-xs text-slate-500">
        {issueCount} issue{issueCount !== 1 ? 's' : ''}
        {monitor.latest_failure_at ? ` · last ${formatRelativeTime(monitor.latest_failure_at)}` : ''}
      </p>
      <FailureReasonLine message={monitor.latest_error_message} />
    </div>
  );
}

// ─── Chart Tooltip ─────────────────────────────────────────────────────────────

const TOOLTIP_SERIES: Record<string, { label: string; dot: string; unit: string }> = {
  uptime: { label: 'Uptime', dot: 'bg-emerald-400', unit: '%' },
  responseTime: { label: 'Response time', dot: 'bg-sky-400', unit: 'ms' },
};

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

function CustomTooltip({ active, payload, label }: any) {
  if (!active || !payload?.length) return null;
  return (
    <div className="px-3.5 py-2.5" style={TOOLTIP_STYLE}>
      <p className="mb-1.5 text-xs font-medium text-slate-400">{label}</p>
      <div className="space-y-1">
        {payload.map((entry: any, idx: number) => {
          const series = TOOLTIP_SERIES[entry.name] ?? { label: entry.name, dot: 'bg-slate-400', unit: '' };
          return (
            <div key={idx} className="flex items-center justify-between gap-4">
              <span className="flex items-center gap-1.5 text-xs text-slate-400">
                <span className={`h-1.5 w-1.5 rounded-full ${series.dot}`} />
                {series.label}
              </span>
              <span className="text-sm font-semibold tabular-nums text-white">
                {typeof entry.value === 'number'
                  ? entry.name === 'responseTime'
                    ? Math.round(entry.value)
                    : entry.value.toFixed(2)
                  : entry.value}
                {series.unit}
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
    <div className="animate-pulse space-y-6">
      <div className="flex items-center justify-between">
        <div className="h-8 w-40 rounded-lg bg-well-hover" />
        <div className="hidden h-9 w-48 rounded-lg bg-well-hover sm:block" />
      </div>
      <div className="glass h-36" />
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.8fr)]">
        <div className="glass h-[22rem]" />
        <div className="glass h-[22rem]" />
      </div>
      <div className="grid gap-6 xl:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.8fr)]">
        <div className="glass h-72" />
        <div className="glass h-72" />
      </div>
    </div>
  );
}

function EmptyState({ message, sub }: { message: string; sub?: string }) {
  return (
    <div className="flex min-h-[168px] flex-1 flex-col items-center justify-center px-5 pb-5 text-center">
      <p className="text-sm font-medium text-slate-300">{message}</p>
      {sub && <p className="mt-1 max-w-sm text-[13px] text-slate-500">{sub}</p>}
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
    <div className={`glass flex min-w-0 flex-col overflow-hidden ${className ?? ''}`}>
      <div className="flex min-w-0 shrink-0 flex-wrap items-center justify-between gap-2 px-5 py-4">
        <h3 className="min-w-0 text-[15px] font-semibold text-white">{title}</h3>
        {action && <div className="min-w-0 text-[13px] text-slate-500">{action}</div>}
      </div>
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">{children}</div>
    </div>
  );
}

function OperationalSummaryCard({ summary }: { summary: OperationalSummary }) {
  const dot =
    summary.tone === 'critical'
      ? 'bg-rose-500'
      : summary.tone === 'attention'
        ? 'bg-amber-500'
        : 'bg-emerald-500';

  return (
    <section className="glass grid gap-6 p-5 lg:grid-cols-[minmax(0,1fr)_minmax(0,2fr)] lg:items-center">
      <div className="min-w-0">
        <div className="flex items-center gap-2.5">
          <span className={`h-2 w-2 shrink-0 rounded-full ${dot}`} />
          <h2 className="text-lg font-semibold text-white">{summary.label}</h2>
        </div>
        <p className="mt-1.5 max-w-xl text-sm text-slate-400">{summary.description}</p>
        <Button
          variant="ghost"
          asChild
          className="mt-4"
          iconTrailing={<ArrowRight className="h-4 w-4" strokeWidth={1.75} />}
        >
          <Link href={summary.primaryActionHref}>{summary.primaryActionLabel}</Link>
        </Button>
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
            <div key={metric.label} className="glass-well p-4">
              <p className="text-xs text-slate-400">{metric.label}</p>
              <p className={`mt-1 text-2xl font-semibold tabular-nums ${valueTone}`}>{metric.value}</p>
              {metric.detail && <p className="mt-1 text-xs text-slate-500">{metric.detail}</p>}
            </div>
          );
        })}
      </div>
    </section>
  );
}

function RangeControls({
  timeRange,
  setTimeRange,
}: {
  timeRange: DashboardPageRange;
  setTimeRange: (range: DashboardPageRange) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {DASHBOARD_RANGE_OPTIONS.map((range) => (
        <FilterChip
          key={range}
          selected={timeRange === range}
          onClick={() => setTimeRange(range)}
        >
          {range}
        </FilterChip>
      ))}
    </div>
  );
}

function UptimeResponseChart({
  trendData,
  hasEnoughTrendData,
  timeRange,
  setTimeRange,
  noMatchingMonitors,
}: {
  trendData: TrendPoint[];
  hasEnoughTrendData: boolean;
  timeRange: DashboardPageRange;
  setTimeRange: (range: DashboardPageRange) => void;
  noMatchingMonitors: boolean;
}) {
  const uptimeAxis = useMemo(() => {
    const values = trendData
      .map((point) => point.uptime)
      .filter((value): value is number => typeof value === 'number');
    const min = values.length === 0 ? 98.5 : Math.min(...values);
    const padded = Math.max(0, min - Math.max(0.25, (100 - min) * 0.08));
    // Evenly spaced ticks ending at 100%, so the axis never skips a label.
    const step = UPTIME_TICK_STEPS.find((s) => (100 - padded) / s <= 4) ?? 25;
    const count = Math.max(1, Math.ceil((100 - padded) / step - 1e-9));
    const ticks = Array.from({ length: count + 1 }, (_, i) => Math.round((100 - step * (count - i)) * 10) / 10);
    return { domain: [ticks[0], 100] as [number, number], ticks };
  }, [trendData]);

  return (
    <section className="glass min-w-0 overflow-hidden">
      <div className="flex flex-col gap-3 px-5 py-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h3 className="text-[15px] font-semibold text-white">Uptime & response time</h3>
            <InfoTip
              entries={[{ label: 'Metric', value: 'Uptime and average HTTP latency for the selected range' }]}
              title="Uptime & response time"
            />
          </div>
          <div className="mt-1.5 flex flex-wrap items-center gap-4 text-xs text-slate-400">
            <span className="flex items-center gap-1.5">
              <span className="h-2 w-2 rounded-full bg-emerald-500" /> Uptime (%)
            </span>
            <span className="flex items-center gap-1.5">
              <span className="h-2 w-2 rounded-full bg-sky-500" /> Response time (ms)
            </span>
          </div>
        </div>
        <RangeControls timeRange={timeRange} setTimeRange={setTimeRange} />
      </div>

      <div className="px-5 pb-5">
        {hasEnoughTrendData ? (
          <div className="h-64 min-w-0">
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={trendData} margin={{ top: 10, right: 6, bottom: 0, left: 4 }}>
                <defs>
                  <linearGradient id="combinedUptimeGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor="#46d17f" stopOpacity={0.22} />
                    <stop offset="100%" stopColor="#46d17f" stopOpacity={0} />
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
                  width={48}
                  axisLine={false}
                  tickLine={false}
                  tick={AXIS_TICK}
                  tickFormatter={(v) => `${Math.round(Number(v))}ms`}
                />
                <Tooltip content={<CustomTooltip />} cursor={{ stroke: 'var(--glass-edge-strong)', strokeDasharray: '3 3' }} />
                <Area
                  yAxisId="uptime"
                  type="monotone"
                  dataKey="uptime"
                  stroke="#46d17f"
                  strokeWidth={2}
                  fill="url(#combinedUptimeGradient)"
                  name="uptime"
                  activeDot={{ r: 3.5, fill: '#46d17f', stroke: 'var(--bg-elevated)', strokeWidth: 2 }}
                />
                <Line
                  yAxisId="latency"
                  type="monotone"
                  dataKey="responseTime"
                  stroke="#6fb5dd"
                  strokeWidth={2}
                  dot={false}
                  name="responseTime"
                  activeDot={{ r: 3.5, fill: '#6fb5dd', stroke: 'var(--bg-elevated)', strokeWidth: 2 }}
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
              <p className="mt-1 text-[13px] text-slate-500">
                {noMatchingMonitors ? 'Choose fewer tags to widen the dashboard scope.' : 'Trend lines will appear after at least two populated buckets.'}
              </p>
            </div>
          </div>
        )}
      </div>
    </section>
  );
}

function NeedsAttentionPanel({
  monitors,
  loading,
  noMatchingMonitors,
}: {
  monitors: DashboardProblemMonitor[];
  loading: boolean;
  noMatchingMonitors: boolean;
}) {
  // Side by side with the chart (xl), the chart sets the row height and this
  // list scrolls inside it, so the two cards always end on the same line.
  return (
    <div className="relative min-w-0">
      <SectionCard
        className="xl:absolute xl:inset-0"
        title="Needs attention"
        action={monitors.length > 0 ? <Pill tone="danger" size="xs" className="tabular-nums">{monitors.length}</Pill> : undefined}
      >
        {loading ? (
          <SectionLoadingState message="Loading monitors needing attention…" />
        ) : monitors.length > 0 ? (
          <div className={`${SCROLL_LIST} max-h-80 min-h-0 flex-1 xl:max-h-none`}>
            {monitors.map((monitor) => (
              <ProblemMonitorItem key={monitor.monitor_id} monitor={monitor} />
            ))}
          </div>
        ) : (
          <EmptyState
            message={noMatchingMonitors ? 'No monitors match these tags' : 'No monitors need immediate action'}
            sub={noMatchingMonitors ? 'Choose fewer tags to widen the dashboard scope.' : 'Resolved history appears in What changed.'}
          />
        )}
      </SectionCard>
    </div>
  );
}


function timelineIcon(item: ActivityTimelineItem) {
  const color =
    item.tone === 'danger'
      ? 'text-rose-400'
      : item.tone === 'warning'
        ? 'text-amber-400'
        : item.tone === 'success'
          ? 'text-emerald-400'
          : 'text-sky-400';

  const Icon =
    item.tone === 'danger'
      ? AlertTriangle
      : item.tone === 'warning'
        ? Activity
        : item.tone === 'success'
          ? Check
          : Clock3;

  return (
    <Icon className={`mt-0.5 h-4 w-4 shrink-0 ${color}`} strokeWidth={1.75} />
  );
}

function WhatChangedTimeline({
  items,
  loading,
}: {
  items: ActivityTimelineItem[];
  loading: boolean;
}) {
  return (
    <SectionCard
      title="What changed"
      action={
        <Link href="/alerts" className="inline-flex items-center gap-1.5 text-cyan-300 transition-colors hover:text-cyan-200">
          View all <ArrowRight className="h-3.5 w-3.5" strokeWidth={1.75} />
        </Link>
      }
    >
      {loading ? (
        <SectionLoadingState message="Loading recent activity…" />
      ) : items.length > 0 ? (
        <div className={`${SCROLL_LIST} max-h-80`}>
          {items.map((item) => (
            <div key={item.id} className="flex gap-3 px-5 py-3.5">
              {timelineIcon(item)}
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 items-start justify-between gap-3">
                  <div className="flex min-w-0 items-start gap-2">
                    <p className="text-sm font-medium text-white">{item.label}</p>
                    {item.reason && (
                      <InfoTip
                        title={item.title}
                        entries={[
                          { label: 'Detail', value: item.detail || '—' },
                          { label: 'Reason', value: <span className="break-words">{item.reason}</span> },
                        ]}
                      />
                    )}
                  </div>
                  <span className="shrink-0 text-xs text-slate-500">{item.relativeTime}</span>
                </div>
                <p className="mt-0.5 break-words text-[13px] text-slate-300">
                  {item.title}
                  {item.detail && <span className="text-slate-500"> · {item.detail}</span>}
                </p>
                <FailureReasonLine message={item.reason} />
              </div>
            </div>
          ))}
        </div>
      ) : (
        <EmptyState message="No recent activity" sub="Failures and alerts will appear here when checks change state." />
      )}
    </SectionCard>
  );
}

// ─── Dashboard Page ────────────────────────────────────────────────────────────

export default function DashboardPage() {
  const [dashboard, setDashboard] = useState<DashboardSummaryResponse | null>(null);
  const [problemMonitorsData, setProblemMonitorsData] = useState<DashboardProblemMonitor[] | null>(null);
  const [recentFailuresData, setRecentFailuresData] = useState<DashboardFailureEvent[] | null>(null);
  const [recentAlertsData, setRecentAlertsData] = useState<Alert[] | null>(null);
  const [tenantRetentionDays, setTenantRetentionDays] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
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
        getDashboardProblemMonitors({ range, tags }),
        getDashboardRecentFailures({ range, limit: DASHBOARD_LIST_LIMIT[range], tags }),
        getDashboardRecentAlerts({ range, limit: DASHBOARD_LIST_LIMIT[range], tags }),
      ]);

      if (requestSequenceRef.current !== requestID) {
        return;
      }

      setProblemMonitorsData(problemResponse.problem_monitors);
      setRecentFailuresData(failuresResponse.recent_failures);
      setRecentAlertsData(alertsResponse.recent_alerts);
    } catch (err) {
      console.error('Failed to load dashboard secondary sections:', err);
      if (requestSequenceRef.current !== requestID) {
        return;
      }
      setProblemMonitorsData([]);
      setRecentFailuresData([]);
      setRecentAlertsData([]);
    }
  }, []);

  const loadData = useCallback(async () => {
    const requestID = requestSequenceRef.current + 1;
    requestSequenceRef.current = requestID;

    try {
      setLoading(true);
      setError(null);
      setProblemMonitorsData(null);
      setRecentFailuresData(null);
      setRecentAlertsData(null);

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
        setProblemMonitorsData([]);
        setRecentFailuresData([]);
        setRecentAlertsData([]);
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
  const problemMonitors = useMemo(() => problemMonitorsData || [], [problemMonitorsData]);
  const recentFailures = useMemo(() => recentFailuresData || [], [recentFailuresData]);
  const recentAlerts = useMemo(() => recentAlertsData || [], [recentAlertsData]);

  const stats = dashboard?.stats;
  const totalMonitors = stats?.total_monitors || 0;
  const hasTagFilter = selectedTags.length > 0;
  const noMatchingMonitors = hasTagFilter && totalMonitors === 0;
  const operationalSummary = useMemo(
    () => buildOperationalSummary({
      opsSummary,
      problemMonitorsCount: problemMonitors.length,
      recentFailuresCount: recentFailures.length,
      overallUptime: stats?.overall_uptime ?? null,
      avgResponseMs: stats?.avg_response_ms || 0,
      rangeLabel: DASHBOARD_RANGE_LABELS[timeRange],
    }),
    [opsSummary, problemMonitors.length, recentFailures.length, stats?.avg_response_ms, stats?.overall_uptime, timeRange],
  );
  const needsAttention = useMemo(() => sortNeedsAttention(problemMonitors), [problemMonitors]);
  const timelineItems = useMemo(
    () => buildActivityTimeline({ failures: recentFailures, alerts: recentAlerts }).slice(0, 8),
    [recentAlerts, recentFailures],
  );

  const selectedRangeDays = useMemo(() => {
    if (timeRange === '1h') return 0;
    if (timeRange === '24h') return 1;
    if (timeRange === '7d') return 7;
    if (timeRange === '30d') return 30;
    return 90;
  }, [timeRange]);
  const showRetentionWarning =
    tenantRetentionDays !== null && tenantRetentionDays > 0 && selectedRangeDays > tenantRetentionDays;

  if (loading) return <LoadingSkeleton />;

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
            <TagFilterPicker
              availableTags={availableTags}
              selectedTags={selectedTags}
              onToggleTag={toggleTag}
              onClear={clearTags}
            />
            <IconButton icon={<RefreshCw strokeWidth={1.75} />} label="Refresh dashboard" onClick={loadData} />
            <Button variant="accent" icon={<Plus className="h-4 w-4" strokeWidth={1.75} />} asChild>
              <Link href="/monitors/new">Add monitor</Link>
            </Button>
          </>
        }
      />

      {hasTagFilter && (
        <div className="flex flex-wrap items-center gap-2 text-[13px]">
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

      <div className="dash-enter">
        <OperationalSummaryCard summary={operationalSummary} />
      </div>

      <div className="dash-enter grid gap-6 xl:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.8fr)]">
        <UptimeResponseChart
          trendData={trendData}
          hasEnoughTrendData={hasEnoughTrendData}
          timeRange={timeRange}
          setTimeRange={setTimeRange}
          noMatchingMonitors={noMatchingMonitors}
        />
        <NeedsAttentionPanel
          monitors={needsAttention}
          loading={problemMonitorsData === null}
          noMatchingMonitors={noMatchingMonitors}
        />
      </div>

      <div className="dash-enter grid items-start gap-6 xl:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.8fr)]">
        <ServiceGroupsPanel
          groupTags={dashboard?.group_tags ?? []}
          groups={dashboard?.groups ?? []}
          range={timeRange as DashboardRange}
          filterTags={selectedTags}
        />
        <WhatChangedTimeline
          items={timelineItems}
          loading={recentFailuresData === null || recentAlertsData === null}
        />
      </div>
    </div>
  );
}
