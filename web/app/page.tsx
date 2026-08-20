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
  OperationalSummary,
  buildActivityTimeline,
  buildOperationalSummary,
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
      <button
        onClick={() => setOpen((value) => !value)}
        className={`inline-flex h-9 items-center gap-2 rounded-lg border px-3 text-xs font-medium transition-colors ${
          hasSelection
            ? 'border-cyan-500/40 bg-cyan-500/10 text-cyan-300'
            : 'border-white/[0.06] bg-slate-900/50 text-slate-300 hover:text-white'
        }`}
      >
        <Tags className="h-3.5 w-3.5" strokeWidth={1.8} />
        <span>Tags</span>
        {hasSelection && (
          <span className="rounded-full bg-cyan-500/20 px-1.5 py-0.5 text-[10px] text-cyan-200">
            {selectedTags.length}
          </span>
        )}
        <ChevronDown className={`h-3 w-3 transition-transform ${open ? 'rotate-180' : ''}`} strokeWidth={2} />
      </button>

      {open && (
        <div className="absolute right-0 z-50 mt-2 w-80 rounded-xl border border-white/[0.08] bg-slate-950/95 p-3 shadow-2xl backdrop-blur">
          <div className="mb-3 flex items-center justify-between">
            <p className="text-[11px] font-semibold uppercase tracking-wider text-slate-500">Filter dashboard by tags</p>
            {hasSelection && (
              <button onClick={onClear} className="text-[11px] text-slate-400 transition-colors hover:text-white">
                Clear all
              </button>
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

function SectionLoadingState({ message }: { message: string }) {
  return (
    <div className="flex h-40 items-center justify-center rounded-lg border border-dashed border-white/[0.08] bg-slate-950/25">
      <div className="flex items-center gap-2 text-sm text-slate-500">
        <RefreshCw className="h-3.5 w-3.5 animate-spin" strokeWidth={1.8} />
        <span>{message}</span>
      </div>
    </div>
  );
}

// ─── Status Pill ───────────────────────────────────────────────────────────────

const STATUS_TONE: Record<string, PillTone> = {
  success: 'success',
  error: 'warning',
  failure: 'danger',
};

function StatusPill({ status }: { status: string | null | undefined }) {
  const normalized = status || 'paused';
  const tone = STATUS_TONE[normalized] ?? 'neutral';
  return (
    <Pill tone={tone} size="xs" className="uppercase tracking-wider">
      {normalized}
    </Pill>
  );
}

// ─── Problem Monitor Item ──────────────────────────────────────────────────────

function ProblemMonitorItem({ monitor }: { monitor: DashboardProblemMonitor }) {
  const issueCount = monitor.failure_count + monitor.error_count;
  const uptimePct = Math.max(0, Math.min(100, monitor.uptime));
  const uptimeGood = uptimePct >= 99;
  const uptimeMid = uptimePct >= 95;
  const barColor = uptimeGood
    ? 'bg-emerald-400'
    : uptimeMid
      ? 'bg-amber-400'
      : 'bg-rose-400';
  const trackColor = uptimeGood
    ? 'bg-slate-800/80'
    : uptimeMid
      ? 'bg-amber-500/10'
      : 'bg-rose-500/10';
  const uptimeTextColor = uptimeGood
    ? 'text-emerald-400'
    : uptimeMid
      ? 'text-amber-400'
      : 'text-rose-400';
  const barWidth = uptimePct > 0 ? Math.max(uptimePct, 2) : 0;

  const infoEntries: PopoverEntry[] = [
    { label: 'Failures', value: monitor.failure_count },
    { label: 'Errors', value: monitor.error_count },
    { label: 'Total issues', value: issueCount },
    { label: 'Latest issue', value: monitor.latest_failure_at ? formatRelativeTime(monitor.latest_failure_at) : '—' },
    { label: 'Uptime', value: `${uptimePct.toFixed(2)}%` },
  ];

  return (
    <div className="-mx-2 rounded-lg px-2 py-3.5 transition-colors hover:bg-white/[0.025]">
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-start gap-2">
            <Link
              href={`/monitors/${monitor.monitor_id}`}
              className="min-w-0 whitespace-normal break-all text-sm font-medium text-white transition-colors hover:text-cyan-400"
            >
              {monitor.monitor_name}
            </Link>
            <InfoTip entries={infoEntries} title={monitor.monitor_name} />
          </div>
          <div className="mt-2.5 flex items-center gap-3">
            {/* Uptime bar */}
            <div className={`h-1.5 flex-1 overflow-hidden rounded-full ${trackColor}`}>
              <div
                className={`h-full rounded-full ${barColor} transition-all`}
                style={{ width: `${barWidth}%` }}
              />
            </div>
            <span className={`shrink-0 text-xs font-medium tabular-nums ${uptimeTextColor}`}>
              {uptimePct.toFixed(1)}%
            </span>
          </div>
          <p className="mt-1 text-[11px] text-slate-500">
            {issueCount} issue{issueCount !== 1 ? 's' : ''}
            {monitor.latest_failure_at ? ` · last ${formatRelativeTime(monitor.latest_failure_at)}` : ''}
          </p>
        </div>
        <StatusPill status={monitor.current_status} />
      </div>
    </div>
  );
}

// ─── Chart Tooltip ─────────────────────────────────────────────────────────────

const TOOLTIP_SERIES: Record<string, { label: string; dot: string; unit: string }> = {
  uptime: { label: 'Uptime', dot: 'bg-emerald-400', unit: '%' },
  responseTime: { label: 'Response time', dot: 'bg-sky-400', unit: 'ms' },
};

function CustomTooltip({ active, payload, label }: any) {
  if (!active || !payload?.length) return null;
  return (
    <div className="rounded-lg border border-white/10 bg-slate-900/95 px-3.5 py-2.5 shadow-2xl backdrop-blur">
      <p className="mb-1.5 text-[11px] font-medium uppercase tracking-wider text-slate-500">{label}</p>
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
    <div className="animate-pulse space-y-4">
      <div className="flex items-center justify-between">
        <div className="space-y-2">
          <div className="h-6 w-40 rounded-lg bg-slate-800/60" />
          <div className="h-4 w-72 rounded-lg bg-slate-800/40" />
        </div>
        <div className="hidden h-9 w-48 rounded-lg bg-slate-800/50 sm:block" />
      </div>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {[...Array(4)].map((_, i) => (
          <div key={i} className="h-24 rounded-xl bg-slate-800/50" />
        ))}
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <div className="h-64 rounded-xl bg-slate-800/50" />
        <div className="h-64 rounded-xl bg-slate-800/50" />
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <div className="h-72 rounded-xl bg-slate-800/50" />
        <div className="h-72 rounded-xl bg-slate-800/50" />
      </div>
    </div>
  );
}

function EmptyState({ message, sub }: { message: string; sub?: string }) {
  return (
    <div className="flex min-h-[168px] flex-col items-center justify-center rounded-lg border border-dashed border-white/[0.06] bg-slate-950/20 px-4 text-center">
      <p className="text-sm font-medium text-slate-300">{message}</p>
      {sub && <p className="mt-1 max-w-sm text-xs text-slate-500">{sub}</p>}
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
    <div className={`min-w-0 overflow-hidden rounded-xl border border-white/[0.07] bg-slate-900/55 shadow-subtle ${className ?? ''}`}>
      <div className="flex min-h-14 min-w-0 flex-wrap items-center justify-between gap-2 border-b border-white/[0.05] bg-slate-950/20 px-5 py-3">
        <h3 className="min-w-0 text-sm font-semibold text-white">{title}</h3>
        {action && <div className="min-w-0 text-xs text-slate-500">{action}</div>}
      </div>
      <div className="min-w-0 p-5">{children}</div>
    </div>
  );
}

function OperationalSummaryCard({ summary }: { summary: OperationalSummary }) {
  const toneStyles: Record<OperationalSummary['tone'], { ring: string; icon: string; text: string; iconBg: string; glow: string }> = {
    critical: {
      ring: 'border-rose-500/20',
      icon: 'text-rose-300',
      text: 'text-rose-300',
      iconBg: 'bg-rose-500/10',
      glow: 'rgba(240,74,90, 0.08)',
    },
    attention: {
      ring: 'border-amber-500/20',
      icon: 'text-amber-300',
      text: 'text-amber-300',
      iconBg: 'bg-amber-500/10',
      glow: 'rgba(230,178,63, 0.07)',
    },
    stable: {
      ring: 'border-emerald-500/20',
      icon: 'text-emerald-300',
      text: 'text-emerald-300',
      iconBg: 'bg-emerald-500/10',
      glow: 'rgba(70,209,127, 0.07)',
    },
    clean: {
      ring: 'border-emerald-500/20',
      icon: 'text-emerald-300',
      text: 'text-emerald-300',
      iconBg: 'bg-emerald-500/10',
      glow: 'rgba(70,209,127, 0.07)',
    },
  };
  const tone = toneStyles[summary.tone];

  return (
    <section
      className={`overflow-hidden rounded-xl border ${tone.ring} bg-slate-900/60 shadow-subtle`}
      style={{ backgroundImage: `radial-gradient(ellipse 60% 90% at 8% 0%, ${tone.glow}, transparent)` }}
    >
      <div className="grid gap-5 p-5 lg:grid-cols-[minmax(0,1.1fr)_minmax(0,1.9fr)]">
        <div className="flex min-w-0 flex-col gap-4 sm:flex-row">
          <div className={`flex h-16 w-16 shrink-0 items-center justify-center rounded-xl border border-current/25 ${tone.iconBg} ${tone.icon}`}>
            {summary.tone === 'critical' ? (
              <AlertTriangle className="h-8 w-8" strokeWidth={1.8} />
            ) : (
              <Check className="h-8 w-8" strokeWidth={2.3} />
            )}
          </div>
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="relative flex h-2.5 w-2.5">
                {summary.tone === 'critical' && (
                  <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-rose-400 opacity-60" />
                )}
                <span className={`relative inline-flex h-2.5 w-2.5 rounded-full ${summary.tone === 'critical' ? 'bg-rose-400' : summary.tone === 'attention' ? 'bg-amber-400' : 'bg-emerald-400'}`} />
              </span>
              <h2 className={`text-lg font-semibold tracking-tight ${tone.text}`}>{summary.label}</h2>
            </div>
            <p className="mt-2 max-w-xl text-sm leading-6 text-slate-400">{summary.description}</p>
            <p className={`mt-2 text-sm font-medium ${summary.attentionCount > 0 ? 'text-amber-300' : 'text-slate-500'}`}>
              {summary.attentionCount > 0
                ? `${summary.attentionCount} monitor${summary.attentionCount === 1 ? '' : 's'} need attention.`
                : 'No monitors need attention.'}
            </p>
            <Link
              href={summary.primaryActionHref}
              className="mt-4 inline-flex items-center gap-2 rounded-lg border border-white/[0.08] bg-white/[0.04] px-3.5 py-2 text-sm font-medium text-white transition-colors hover:bg-white/[0.08]"
            >
              {summary.primaryActionLabel}
              <ArrowRight className="h-4 w-4" strokeWidth={1.8} />
            </Link>
          </div>
        </div>

        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          {summary.metrics.map((metric) => {
            const metricTone =
              metric.tone === 'critical'
                ? 'text-rose-300'
                : metric.tone === 'attention'
                  ? 'text-amber-300'
                  : metric.tone === 'clean'
                    ? 'text-emerald-300'
                    : 'text-white';
            return (
              <div key={metric.label} className="min-h-24 rounded-lg border border-white/[0.06] bg-slate-950/35 p-4 transition-colors hover:border-white/[0.12]">
                <p className="text-[11px] font-medium uppercase tracking-wider text-slate-500">{metric.label}</p>
                <p className={`mt-2 text-2xl font-semibold tabular-nums ${metricTone}`}>{metric.value}</p>
                {metric.detail && <p className="mt-1 text-xs text-slate-500">{metric.detail}</p>}
              </div>
            );
          })}
        </div>
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
  const uptimeDomain = useMemo<[number, number]>(() => {
    const values = trendData
      .map((point) => point.uptime)
      .filter((value): value is number => typeof value === 'number');
    if (values.length === 0) return [98.5, 100];
    const min = Math.min(...values);
    const padded = min - Math.max(0.25, (100 - min) * 0.08);
    return [Math.max(0, Math.floor(padded * 10) / 10), 100];
  }, [trendData]);

  return (
    <section className="overflow-hidden rounded-xl border border-white/[0.07] bg-slate-900/55 shadow-subtle">
      <div className="flex flex-col gap-3 border-b border-white/[0.05] bg-slate-950/20 px-5 py-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <div className="flex items-center gap-2">
            <h3 className="text-base font-semibold text-white">Uptime & response time</h3>
            <InfoTip
              entries={[{ label: 'Metric', value: 'Uptime and average HTTP latency for the selected range' }]}
              title="Uptime & response time"
            />
          </div>
          <div className="mt-2 flex flex-wrap items-center gap-4 text-xs">
            <span className="flex items-center gap-1.5 text-emerald-300">
              <span className="h-2 w-2 rounded-full bg-emerald-400" /> Uptime (%)
            </span>
            <span className="flex items-center gap-1.5 text-sky-300">
              <span className="h-2 w-2 rounded-full bg-sky-400" /> Response time (ms)
            </span>
          </div>
        </div>
        <RangeControls timeRange={timeRange} setTimeRange={setTimeRange} />
      </div>

      <div className="p-5">
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
                <CartesianGrid stroke="rgba(255,255,255,0.04)" vertical={false} />
                <XAxis dataKey="date" axisLine={false} tickLine={false} tick={{ fill: '#64748b', fontSize: 11 }} minTickGap={24} />
                <YAxis
                  yAxisId="uptime"
                  domain={uptimeDomain}
                  width={58}
                  axisLine={false}
                  tickLine={false}
                  tick={{ fill: '#64748b', fontSize: 11 }}
                  tickFormatter={(v) => `${Number(v).toFixed(1).replace(/\.0$/, '')}%`}
                />
                <YAxis
                  yAxisId="latency"
                  orientation="right"
                  width={48}
                  axisLine={false}
                  tickLine={false}
                  tick={{ fill: '#64748b', fontSize: 11 }}
                  tickFormatter={(v) => `${Math.round(Number(v))}ms`}
                />
                <Tooltip content={<CustomTooltip />} cursor={{ stroke: 'rgba(174,182,194,0.25)', strokeDasharray: '3 3' }} />
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
          <div className="flex h-48 items-center justify-center rounded-lg border border-dashed border-white/[0.06] bg-slate-950/25 px-4 text-center">
            <div>
              <p className="text-sm font-medium text-slate-300">
                {noMatchingMonitors ? 'No monitors match these tags' : 'Not enough trend data yet'}
              </p>
              <p className="mt-1 text-xs text-slate-500">
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
  return (
    <SectionCard
      className="min-w-0"
      title="Needs attention"
      action={monitors.length > 0 ? <span className="rounded-full bg-rose-500/10 px-2 py-0.5 text-rose-300">{monitors.length}</span> : undefined}
    >
      {loading ? (
        <SectionLoadingState message="Loading monitors needing attention..." />
      ) : monitors.length > 0 ? (
        <div className="dashboard-scroll -my-1 max-h-72 divide-y divide-white/[0.04] overflow-y-auto pr-1">
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
  );
}


function timelineIcon(item: ActivityTimelineItem) {
  const classes =
    item.tone === 'danger'
      ? 'border-rose-500/40 bg-rose-950 text-rose-300'
      : item.tone === 'warning'
        ? 'border-amber-500/40 bg-amber-950 text-amber-300'
        : item.tone === 'success'
          ? 'border-emerald-500/40 bg-emerald-950 text-emerald-300'
          : 'border-sky-500/40 bg-sky-950 text-sky-300';

  const Icon =
    item.tone === 'danger'
      ? AlertTriangle
      : item.tone === 'warning'
        ? Activity
        : item.tone === 'success'
          ? Check
          : Clock3;

  return (
    <span className={`relative z-10 flex h-7 w-7 shrink-0 items-center justify-center rounded-full border ${classes}`}>
      <Icon className="h-3.5 w-3.5" strokeWidth={2} />
    </span>
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
        <Link href="/alerts" className="inline-flex items-center gap-1.5 text-cyan-400 transition-colors hover:text-cyan-300">
          View all <ArrowRight className="h-3.5 w-3.5" />
        </Link>
      }
    >
      {loading ? (
        <SectionLoadingState message="Loading recent activity..." />
      ) : items.length > 0 ? (
        <div className="dashboard-scroll -my-1 max-h-80 overflow-y-auto pr-1">
          <div className="relative space-y-4">
            <div className="absolute bottom-3 left-3.5 top-3 w-px bg-white/[0.08]" />
            {items.map((item) => (
              <div key={item.id} className="relative flex gap-3">
                {timelineIcon(item)}
                <div className="min-w-0 flex-1 border-b border-white/[0.04] pb-4">
                  <div className="flex min-w-0 items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="text-sm font-medium text-white">{item.label}</p>
                      <p className="mt-0.5 break-words text-sm text-slate-300">{item.title}</p>
                      {item.detail && <p className="mt-0.5 text-xs text-slate-500">{item.detail}</p>}
                    </div>
                    <span className="shrink-0 text-xs text-slate-500">{item.relativeTime}</span>
                  </div>
                </div>
              </div>
            ))}
          </div>
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
    <div className="space-y-4">
      <NotificationNudge />

      {/* Error Banner */}
      {error && (
        <div className="flex items-center justify-between rounded-lg border border-rose-500/20 bg-rose-500/10 px-4 py-3">
          <p className="text-sm text-rose-400">{error}</p>
          <Button variant="danger" size="sm" onClick={loadData}>Retry</Button>
        </div>
      )}

      <div className="flex flex-col gap-3 border-b border-white/[0.06] pb-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-white">Dashboard</h1>
          <p className="mt-1 text-sm text-slate-500">Real-time overview of your monitoring environment</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <TagFilterPicker
            availableTags={availableTags}
            selectedTags={selectedTags}
            onToggleTag={toggleTag}
            onClear={clearTags}
          />
          <Button variant="ghost" icon={<Plus strokeWidth={1.75} />} asChild>
            <Link href="/monitors/new">Add monitor</Link>
          </Button>
          <button
            type="button"
            onClick={loadData}
            className="inline-flex h-9 w-9 items-center justify-center rounded-[12px] border border-white/10 bg-white/[0.04] text-slate-300 transition-colors hover:bg-white/[0.08] hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/40"
            aria-label="Refresh dashboard"
          >
            <RefreshCw className="h-4 w-4" strokeWidth={1.75} />
          </button>
        </div>
      </div>

      {hasTagFilter && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-cyan-500/20 bg-cyan-500/5 px-4 py-3">
          <span className="text-xs font-medium uppercase tracking-wider text-cyan-300">Scoped to</span>
          {selectedTags.map((tag) => (
            <span key={tag} className="rounded-full border border-cyan-500/30 bg-cyan-500/10 px-2 py-0.5 text-xs text-cyan-200">
              {tag}
            </span>
          ))}
          <button onClick={clearTags} className="ml-auto inline-flex items-center gap-1 text-xs text-slate-400 transition-colors hover:text-white">
            <X className="h-3.5 w-3.5" strokeWidth={1.8} />
            Clear
          </button>
        </div>
      )}

      {showRetentionWarning && (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-4 py-3">
          <p className="text-sm text-amber-300">
            Data retention is set to {tenantRetentionDays} day{tenantRetentionDays === 1 ? '' : 's'}.
            Older history is deleted, so this {timeRange} view may be partial.
          </p>
        </div>
      )}

      {noMatchingMonitors && (
        <div className="rounded-lg border border-white/[0.08] bg-slate-900/60 px-4 py-3">
          <p className="text-sm text-slate-300">No monitors match the selected tag combination.</p>
          <p className="mt-1 text-xs text-slate-500">Adjust the tag filter to broaden the dashboard scope.</p>
        </div>
      )}

      <div className="dash-enter">
        <OperationalSummaryCard summary={operationalSummary} />
      </div>

      <div className="dash-enter grid items-start gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.8fr)]" style={{ animationDelay: '80ms' }}>
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

      <div className="dash-enter grid items-start gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(340px,0.8fr)]" style={{ animationDelay: '160ms' }}>
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
