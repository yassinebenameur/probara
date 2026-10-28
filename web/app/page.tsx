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
import LiveStatus from '@/components/ui/LiveStatus';
import Page from '@/components/ui/Page';
import PageHeader from '@/components/ui/PageHeader';
import Pill from '@/components/ui/Pill';
import FilterPopover, { type FilterOption } from '@/components/ui/FilterPopover';
import RangePicker from '@/components/ui/RangePicker';
import ScrollList from '@/components/ui/ScrollList';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { RelativeTime } from '@/components/ui/RelativeTime';
import { FOCUS_RING, FOCUS_RING_INSET } from '@/components/ui/focus';
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
import { Plus, Tag, X } from 'lucide-react';
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
import { formatLatency, formatUptime, RANGES, rangeStart } from '@/lib/format';
import { shortFailureReason } from '@/lib/monitor-utils';
import {
  MONITOR_STATUS_DOT,
  MONITOR_STATUS_LABEL,
  MONITOR_STATUS_TONE,
  TONE_TEXT,
  UPTIME_TONE_VAR,
  uptimeTone,
} from '@/lib/status-tones';
import { useLiveRefresh, type LiveLoadOptions } from '@/lib/useLiveRefresh';
import { flashStyle, useRowFlash } from '@/lib/useRowFlash';
import { useDocumentTitle } from '@/lib/useDocumentTitle';
import CheckTrace from '@/components/monitors/CheckTrace';
import { getTagColor } from '@/components/monitors/TagPill';
import { CHART_GRID, LATENCY_COLOR } from '@/components/monitors/metric-chart';
import ServiceGroupsPanel from '@/components/dashboard/ServiceGroupsPanel';
import { NotificationNudge } from '@/components/layout/NotificationNudge';

type TrendPoint = { date: string; uptime: number | null; responseTime: number | null; total: number };

const DASHBOARD_RANGE_OPTIONS = ['1h', '24h', '7d', '30d', '90d'] as const;
type DashboardPageRange = (typeof DASHBOARD_RANGE_OPTIONS)[number];

// The API's cap for every dashboard list. What changed keeps one row per
// monitor, so it reads the full cap of failed checks to reach enough monitors;
// the problem list is fetched whole so its states (the degraded count, group
// members) and the Needs attention total are complete. Needs attention draws
// every row and scrolls inside its card.
const DASHBOARD_LIST_LIMIT = 50;

/** What changed rows; the card scrolls, so this only bounds the DOM. */
const TIMELINE_ROWS = 40;

/** Checks per Needs attention trace. */
const TRACE_SLOTS = 20;

// ─── Grid ──────────────────────────────────────────────────────────────────────
//
// One 12-column grid from xl. The first rows have fixed (clamped) heights, so
// the cards in them line up and their lists scroll inside instead of growing
// the page; Service groups takes a full-width row below.
//
//   xl  (1280)  chart 7 | attention 5 (spans two rows)
//               changes 7 |
//   3xl (1920)  chart 5 | attention 4 | changes 3
//   4xl (2400)  chart 6 | attention 3 | changes 3
//
// Below xl the cards stack, Needs attention first (DOM order), and the lists
// cap their own height.
const GRID =
  'grid grid-cols-1 gap-6 xl:grid-cols-12 ' +
  'xl:grid-rows-[clamp(20rem,40vh,26rem)_clamp(20rem,46vh,32rem)_auto] ' +
  '3xl:grid-rows-[clamp(26rem,56vh,40rem)_auto]';
const CELL_ATTENTION =
  'min-h-0 min-w-0 xl:col-span-5 xl:col-start-8 xl:row-span-2 xl:row-start-1 ' +
  '3xl:col-span-4 3xl:col-start-6 3xl:row-span-1 4xl:col-span-3 4xl:col-start-7';
const CELL_CHART = 'min-h-0 min-w-0 xl:col-span-7 xl:col-start-1 xl:row-start-1 3xl:col-span-5 4xl:col-span-6';
const CELL_CHANGES =
  'min-h-0 min-w-0 xl:col-span-7 xl:col-start-1 xl:row-start-2 ' +
  '3xl:col-span-3 3xl:col-start-10 3xl:row-start-1';
const CELL_GROUPS = 'min-w-0 xl:col-span-12 xl:row-start-3 3xl:row-start-2';

/** Lists below xl, where the card has no grid height to fill. */
const LIST_HEIGHT = 'max-h-[32rem] xl:max-h-none';

/**
 * The dashboard's tag scope: the shared FilterPopover (as on /monitors), fed
 * the tags the dashboard reports. It has no per-tag counts, so rows carry
 * none; the page keeps its sorted array, so each change maps back onto
 * toggle/clear. Like /monitors (matchesAnyTag in lib/monitor-list.ts), the
 * dashboard API scopes to monitors carrying any selected tag, so a count and
 * the ?tag= list it links to agree.
 * Sized md to line up with the RangePicker and button beside it.
 */
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
  const options = useMemo<FilterOption[]>(
    () => availableTags.map((tag) => ({ value: tag, label: tag, dotClassName: getTagColor(tag).dot })),
    [availableTags]
  );
  const selected = useMemo(() => new Set(selectedTags), [selectedTags]);

  if (availableTags.length === 0) return null;

  return (
    <FilterPopover
      label="Tags"
      noun="tag"
      icon={<Tag strokeWidth={1.75} />}
      options={options}
      selected={selected}
      size="md"
      popupLabel="Scope to monitors with any of these tags"
      hint="Monitors carrying any selected tag."
      onChange={(next) => {
        if (next.size === 0) {
          onClear();
          return;
        }
        for (const tag of selectedTags) if (!next.has(tag)) onToggleTag(tag);
        for (const tag of next) if (!selected.has(tag)) onToggleTag(tag);
      }}
    />
  );
}

// `relative` keeps sr-only text inside the row: absolutely positioned, it
// would otherwise escape the scroll card's clip and stretch the page.
const ROW_LINK = `relative block px-5 py-2.5 transition-colors duration-fast hover:bg-well active:bg-well-hover focus-visible:bg-well ${FOCUS_RING_INSET}`;

const TEXT_LINK = `rounded text-cyan-300 transition-colors hover:text-cyan-200 hover:underline ${FOCUS_RING}`;

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

// ─── Visible rows ──────────────────────────────────────────────────────────────

/**
 * Ids of rows on screen (inside their scroll card and the viewport), so the
 * per-row trace fetch covers only what can be seen: Needs attention draws up
 * to DASHBOARD_LIST_LIMIT rows but fetches results for the dozen visible.
 * Without IntersectionObserver every row counts as visible.
 */
function useVisibleRows() {
  const [visible, setVisible] = useState<ReadonlySet<string>>(() => new Set());
  const supported = typeof window !== 'undefined' && 'IntersectionObserver' in window;
  const ids = useRef(new Map<Element, string>());
  const observer = useRef<IntersectionObserver | null>(null);

  const getObserver = useCallback(() => {
    if (!supported) return null;
    if (!observer.current) {
      observer.current = new IntersectionObserver(
        (entries) => {
          setVisible((prev) => {
            let next: Set<string> | null = null;
            for (const entry of entries) {
              const id = ids.current.get(entry.target);
              if (!id || entry.isIntersecting === prev.has(id)) continue;
              next ??= new Set(prev);
              if (entry.isIntersecting) next.add(id);
              else next.delete(id);
            }
            return next ?? prev;
          });
        },
        // Fetch a little ahead of the scroll.
        { rootMargin: '160px 0px' },
      );
    }
    return observer.current;
  }, [supported]);

  useEffect(() => () => observer.current?.disconnect(), []);

  const observe = useCallback(
    (el: Element | null, id: string) => {
      const io = getObserver();
      if (!el || !io) return undefined;
      ids.current.set(el, id);
      io.observe(el);
      return () => {
        io.unobserve(el);
        ids.current.delete(el);
        setVisible((prev) => {
          if (!prev.has(id)) return prev;
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
      };
    },
    [getObserver],
  );

  return { visible, observe, supported };
}

// ─── Problem Monitor Item ──────────────────────────────────────────────────────

function ProblemMonitorItem({
  monitor,
  results,
  rangeLabel,
  observe,
  flashClass,
}: {
  monitor: DashboardProblemMonitor;
  results: CheckResult[] | null;
  rangeLabel: string;
  observe: (el: Element | null, id: string) => (() => void) | undefined;
  flashClass: string;
}) {
  const ref = useRef<HTMLAnchorElement>(null);
  useEffect(() => observe(ref.current, monitor.monitor_id), [observe, monitor.monitor_id]);

  const issueCount = monitor.failure_count + monitor.error_count;
  // The monitor's current state, never inferred from its latest check: a
  // monitor failing from one location is degraded whatever its last check did.
  const status = problemMonitorStatus(monitor);
  const tone = MONITOR_STATUS_TONE[status];

  // Two lines: state, name, trace and uptime; then the state in words, the
  // short reason and how much and how recently it failed.
  return (
    <Link
      ref={ref}
      href={`/monitors/${monitor.monitor_id}`}
      className={`${ROW_LINK} ${flashClass}`}
      style={flashStyle(tone)}
    >
      <div className="flex min-w-0 items-center gap-3">
        <span className={`h-2 w-2 shrink-0 rounded-full ${MONITOR_STATUS_DOT[status]}`} aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate text-sm font-medium text-white">{monitor.monitor_name}</span>
        <CheckTrace results={results} slots={TRACE_SLOTS} className="hidden w-[80px] shrink-0 sm:flex" />
        <span
          className="w-12 shrink-0 text-right text-ui font-medium tabular-nums text-slate-300"
          title={`Uptime, ${rangeLabel.toLowerCase()}`}
        >
          <span className="sr-only">Uptime </span>
          {formatUptime(monitor.uptime)}
        </span>
      </div>
      <div className="mt-0.5 flex min-w-0 items-center gap-2 pl-5 text-xs">
        <span className={`shrink-0 font-medium ${TONE_TEXT[tone]}`}>{MONITOR_STATUS_LABEL[status]}</span>
        <ReasonText message={monitor.latest_error_message} />
        <span className="ml-auto flex shrink-0 gap-2 tabular-nums text-slate-500">
          <span>{issueCount} failed</span>
          {monitor.latest_failure_at && <RelativeTime value={monitor.latest_failure_at} />}
        </span>
      </div>
      <CheckTrace results={results} slots={TRACE_SLOTS} className="mt-1.5 pl-5 sm:hidden" />
    </Link>
  );
}

// ─── Chart Tooltip ─────────────────────────────────────────────────────────────

const TOOLTIP_STYLE: React.CSSProperties = {
  background: 'var(--glass-fill-strong)',
  border: '1px solid var(--glass-edge-strong)',
  borderRadius: 12,
  boxShadow: 'var(--glass-shadow-overlay)',
  color: 'var(--text-primary)',
};

const AXIS_TICK = { fill: 'var(--text-muted)', fontSize: 11 };

const UPTIME_TICK_STEPS = [0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 25];

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

// Mirrors the page: header, KPI strip, the grid's first rows and the groups
// row, so the first paint does not jump.
function LoadingSkeleton() {
  return (
    <Page width="data" className="animate-pulse">
      <div role="status" aria-label="Loading dashboard" className="space-y-6">
        <div className="mb-7 flex items-center justify-between">
          <div className="h-8 w-40 rounded-lg bg-well-hover" />
          <div className="hidden h-9 w-72 rounded-lg bg-well-hover sm:block" />
        </div>
        <div className="glass h-36" />
        <div className={GRID}>
          <div className={`glass h-[22rem] xl:h-auto ${CELL_ATTENTION}`} />
          <div className={`glass h-[20rem] xl:h-auto ${CELL_CHART}`} />
          <div className={`glass h-[20rem] xl:h-auto ${CELL_CHANGES}`} />
          <div className={`glass h-40 ${CELL_GROUPS}`} />
        </div>
      </div>
    </Page>
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
    <section className={`glass flex min-w-0 flex-col overflow-hidden xl:h-full ${className ?? ''}`}>
      <div className="flex min-w-0 shrink-0 flex-wrap items-center justify-between gap-2 px-5 py-4">
        <h2 className="min-w-0 text-title font-semibold text-white">{title}</h2>
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

type Metric = OperationalSummary['metrics'][number];

function MetricTile({ metric }: { metric: Metric }) {
  // Healthy values stay neutral so only a problem carries colour.
  const valueTone =
    metric.tone === 'critical'
      ? 'text-rose-300'
      : metric.tone === 'attention'
        ? 'text-amber-300'
        : 'text-white';
  const body = (
    <>
      <p className="truncate text-xs text-slate-400">{metric.label}</p>
      {/* Keyed on the value: a changed number rises in once, the true value
          at once (no count-up). */}
      <p key={metric.value} className={`mt-0.5 animate-value-in text-xl font-semibold tabular-nums sm:text-metric ${valueTone}`}>
        {metric.value}
      </p>
      {metric.detail && <p className="mt-0.5 hidden truncate text-xs text-slate-500 sm:block">{metric.detail}</p>}
    </>
  );
  const base = `glass-well min-w-0 px-3.5 py-3 ${metric.wideOnly ? 'hidden 3xl:block' : ''}`;
  return metric.href ? (
    <Link
      href={metric.href}
      title={metric.title}
      className={`${base} transition-colors duration-fast hover:bg-well-hover active:scale-[0.99] ${FOCUS_RING}`}
    >
      {body}
    </Link>
  ) : (
    <div className={base} title={metric.title}>
      {body}
    </div>
  );
}

function OperationalSummaryCard({ summary }: { summary: OperationalSummary }) {
  const action = summary.primaryAction;

  return (
    <section className="glass grid gap-4 p-4 sm:p-5 lg:grid-cols-[minmax(18rem,26rem)_minmax(0,1fr)] lg:items-center lg:gap-6">
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
                className={`rounded text-cyan-300 underline decoration-cyan-500/40 underline-offset-4 transition-colors sm:text-white sm:no-underline sm:hover:text-cyan-300 sm:hover:underline ${FOCUS_RING}`}
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
          <div className="mt-3 hidden pl-[1.125rem] sm:block">
            <Button variant="ghost" size="sm" asChild>
              <Link href={action.href}>{action.label}</Link>
            </Button>
          </div>
        )}
      </div>

      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4 lg:grid-cols-2 xl:grid-cols-4 3xl:grid-cols-6">
        {summary.metrics.map((metric) => (
          <MetricTile key={metric.label} metric={metric} />
        ))}
      </div>
    </section>
  );
}

function chartSummary(trendData: TrendPoint[], rangeLabel: string): string {
  const uptimes = trendData.map((p) => p.uptime).filter((v): v is number => typeof v === 'number');
  const latencies = trendData.map((p) => p.responseTime).filter((v): v is number => typeof v === 'number' && v > 0);
  if (uptimes.length === 0) return `Uptime and latency, ${rangeLabel.toLowerCase()}: no data`;
  const avg = uptimes.reduce((a, b) => a + b, 0) / uptimes.length;
  const parts = [`Uptime ${formatUptime(avg)} on average over the ${rangeLabel.toLowerCase()}, lowest ${formatUptime(Math.min(...uptimes))}`];
  if (latencies.length > 0) {
    parts.push(`average HTTP latency ${formatLatency(latencies.reduce((a, b) => a + b, 0) / latencies.length)}`);
  }
  return parts.join('; ');
}

function UptimeResponseChart({
  trendData,
  hasEnoughTrendData,
  noMatchingMonitors,
  rangeLabel,
}: {
  trendData: TrendPoint[];
  hasEnoughTrendData: boolean;
  noMatchingMonitors: boolean;
  rangeLabel: string;
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
    <section className="glass flex min-w-0 flex-col overflow-hidden xl:h-full">
      <div className="shrink-0 px-5 py-4">
        <h2 className="text-title font-semibold text-white">Uptime and latency</h2>
        <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-slate-400">
          <span className="flex items-center gap-1.5">
            <span className="h-2 w-2 rounded-full" style={{ background: uptimeColor }} aria-hidden="true" /> Uptime
          </span>
          <span className="flex items-center gap-1.5">
            <span className="h-2 w-2 rounded-full" style={{ background: LATENCY_COLOR }} aria-hidden="true" /> Avg HTTP latency
          </span>
        </div>
      </div>

      <div className="flex min-h-0 flex-1 flex-col px-5 pb-5">
        {hasEnoughTrendData ? (
          <div className="h-64 min-w-0 xl:h-auto xl:min-h-0 xl:flex-1" role="img" aria-label={chartSummary(trendData, rangeLabel)}>
            <ResponsiveContainer width="100%" height="100%">
              <ComposedChart data={trendData} margin={{ top: 10, right: 6, bottom: 0, left: 4 }}>
                <defs>
                  <linearGradient id="combinedUptimeGradient" x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={uptimeColor} stopOpacity={0.18} />
                    <stop offset="100%" stopColor={uptimeColor} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid {...CHART_GRID} />
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
          <div className="glass-well flex h-64 items-center justify-center px-4 text-center xl:h-auto xl:flex-1">
            <div>
              <p className="text-sm font-medium text-slate-300">
                {noMatchingMonitors ? 'No monitors match these tags' : 'Not enough trend data yet'}
              </p>
              <p className="mt-1 text-ui text-slate-500">
                {noMatchingMonitors ? 'Add or change tags to widen the dashboard scope.' : 'Trend lines will appear after at least two populated buckets.'}
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
  observe,
  flashClass,
}: {
  /** Every problem monitor fetched, sorted; the card scrolls. */
  monitors: DashboardProblemMonitor[];
  traces: Record<string, CheckResult[]>;
  /** Every monitor needing attention (above `monitors.length` only when the list was capped). */
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
  observe: (el: Element | null, id: string) => (() => void) | undefined;
  flashClass: (id: string) => string;
}) {
  const shown = monitors.length;
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
          <ScrollList label="Monitors needing attention" className={`min-h-0 flex-1 ${LIST_HEIGHT}`}>
            <div className="divide-y divide-divider">
              {monitors.map((monitor) => (
                <ProblemMonitorItem
                  key={monitor.monitor_id}
                  monitor={monitor}
                  results={traces[monitor.monitor_id] ?? null}
                  rangeLabel={rangeLabel}
                  observe={observe}
                  flashClass={flashClass(monitor.monitor_id)}
                />
              ))}
            </div>
          </ScrollList>
          {/* Outside the scroll area: always reachable, whatever the list's length. */}
          {unhealthyCount > 0 && (
            <Link
              href={downHref}
              className={`shrink-0 border-t border-divider px-5 py-3 text-sm text-cyan-300 transition-colors hover:bg-well hover:text-cyan-200 ${FOCUS_RING_INSET}`}
            >
              View all {unhealthyCount} down or degraded
            </Link>
          )}
        </>
      ) : (
        <EmptyState
          variant="inline"
          headingLevel={3}
          title={noMatchingMonitors ? 'No monitors match these tags' : 'Nothing needs attention'}
          description={
            noMatchingMonitors
              ? 'Add or change tags to widen the dashboard scope.'
              : `No failed checks in the ${rangeLabel.toLowerCase()}.`
          }
        />
      )}
    </SectionCard>
  );
}

function TimelineRow({ item, flashClass }: { item: ActivityTimelineItem; flashClass: string }) {
  const body = (
    <>
      <div className="flex min-w-0 items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <span className="min-w-0 truncate text-sm font-medium text-white">{item.title}</span>
          <Pill tone={item.tone} size="xs" className="shrink-0">
            {item.event}
          </Pill>
        </div>
        <RelativeTime value={item.occurredAt} className="shrink-0 text-xs tabular-nums text-slate-500" />
      </div>
      {(item.reason || item.detail) && (
        <div className="mt-0.5 flex min-w-0 items-center justify-between gap-3 text-xs">
          <ReasonText message={item.reason} />
          {item.detail && (
            <span className="ml-auto shrink-0 tabular-nums text-slate-500">
              {item.detail}
              {item.detailAt && (
                <>
                  {' '}
                  <RelativeTime value={item.detailAt} />
                </>
              )}
            </span>
          )}
        </div>
      )}
    </>
  );
  const style = flashStyle(item.tone);
  return item.href ? (
    <Link href={item.href} className={`${ROW_LINK} ${flashClass}`} style={style}>
      {body}
    </Link>
  ) : (
    <div className={`px-5 py-2.5 ${flashClass}`} style={style}>
      {body}
    </div>
  );
}

function WhatChangedTimeline({
  items,
  loading,
  loadFailed,
  onRetry,
  rangeLabel,
  flashClass,
}: {
  items: ActivityTimelineItem[];
  loading: boolean;
  /** The activity never loaded: show the failure, not "No changes". */
  loadFailed: boolean;
  onRetry: () => void;
  rangeLabel: string;
  flashClass: (id: string) => string;
}) {
  return (
    <SectionCard
      title="What changed"
      action={
        <Link href="/alerts" className={TEXT_LINK}>
          Alert history
        </Link>
      }
    >
      {loadFailed ? (
        <SectionLoadError onRetry={onRetry} />
      ) : loading ? (
        <TableSkeleton rows={3} columns={1} bare label="Loading recent activity" />
      ) : items.length > 0 ? (
        <ScrollList label="Recent changes" className={`min-h-0 flex-1 ${LIST_HEIGHT}`}>
          <div className="divide-y divide-divider">
            {items.map((item) => (
              <TimelineRow key={item.id} item={item} flashClass={flashClass(item.id)} />
            ))}
          </div>
        </ScrollList>
      ) : (
        <EmptyState
          variant="inline"
          headingLevel={3}
          title="No changes"
          description={`Failing monitors and alerts in the ${rangeLabel.toLowerCase()} appear here.`}
        />
      )}
    </SectionCard>
  );
}

// ─── Dashboard Page ────────────────────────────────────────────────────────────

export default function DashboardPage() {
  useDocumentTitle('Dashboard');
  const canWrite = useCanWrite();
  const [dashboard, setDashboard] = useState<DashboardSummaryResponse | null>(null);
  const [problemMonitorsData, setProblemMonitorsData] = useState<DashboardProblemMonitor[] | null>(null);
  const [recentFailuresData, setRecentFailuresData] = useState<DashboardFailureEvent[] | null>(null);
  const [recentAlertsData, setRecentAlertsData] = useState<Alert[] | null>(null);
  const [traces, setTraces] = useState<Record<string, CheckResult[]>>({});
  const [tenantRetentionDays, setTenantRetentionDays] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  // Set when the panels' data failed to load; a section that has no data then
  // renders the failure instead of an empty (all-clear) state.
  const [sectionsError, setSectionsError] = useState(false);
  const [timeRange, setTimeRange] = useState<DashboardPageRange>('1h');
  const [selectedTags, setSelectedTags] = useState<string[]>([]);
  const requestSequenceRef = useRef(0);
  const hasDataRef = useRef(false);

  // Retention is a tenant setting, not live data: read it once.
  useEffect(() => {
    let cancelled = false;
    getTenantSettings()
      .then((settings) => {
        if (!cancelled) setTenantRetentionDays(settings.data_retention_days);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  /** False when the sections failed; stale responses are dropped. */
  const loadSecondarySections = useCallback(async (
    requestID: number,
    range: DashboardPageRange,
    tags: string[]
  ): Promise<boolean> => {
    try {
      const [problemResponse, failuresResponse, alertsResponse] = await Promise.all([
        getDashboardProblemMonitors({ range, limit: DASHBOARD_LIST_LIMIT, tags }),
        getDashboardRecentFailures({ range, limit: DASHBOARD_LIST_LIMIT, tags }),
        getDashboardRecentAlerts({ range, limit: DASHBOARD_LIST_LIMIT, tags }),
      ]);
      if (requestSequenceRef.current !== requestID) return true;

      setProblemMonitorsData(problemResponse.problem_monitors);
      setRecentFailuresData(failuresResponse.recent_failures);
      setRecentAlertsData(alertsResponse.recent_alerts);
      setSectionsError(false);
      return true;
    } catch (err) {
      console.error('Failed to load dashboard secondary sections:', err);
      if (requestSequenceRef.current !== requestID) return true;
      // Keep whatever was on screen; sections with nothing show the failure.
      setSectionsError(true);
      return false;
    }
  }, []);

  // Every load keeps what is on screen until new data lands; only the first
  // load shows skeletons. A silent (background) refresh that fails leaves the
  // page as it was and LiveStatus says "Update failed"; a visible one (first
  // load, range or tag change, Refresh) raises the error callout.
  const loadData = useCallback(async ({ silent }: LiveLoadOptions): Promise<boolean> => {
    const requestID = requestSequenceRef.current + 1;
    requestSequenceRef.current = requestID;
    const range = timeRange;
    const tags = selectedTags;

    try {
      const response = await getDashboardSummary({ range, tags });
      if (requestSequenceRef.current !== requestID) return true;

      setDashboard(response);
      setError(null);
      hasDataRef.current = true;
      // The summary paints first; the lists follow.
      return await loadSecondarySections(requestID, range, tags);
    } catch (err) {
      console.error('Failed to load dashboard data:', err);
      if (requestSequenceRef.current === requestID && (!silent || !hasDataRef.current)) {
        setError('Failed to load dashboard data');
        setSectionsError(true);
      }
      return false;
    }
  }, [loadSecondarySections, selectedTags, timeRange]);

  // Loads on mount and on range/tag change, then polls every 30s while the
  // tab is visible and reloads (debounced) on alert-stream events.
  const live = useLiveRefresh(loadData);
  const { refresh } = live;
  const retry = useCallback(() => void refresh(), [refresh]);

  const problemMonitors = useMemo(() => problemMonitorsData || [], [problemMonitorsData]);
  const sortedProblems = useMemo(() => sortNeedsAttention(problemMonitors), [problemMonitors]);
  const problemFlash = useRowFlash(sortedProblems, (m) => m.monitor_id, (m) => problemMonitorStatus(m));

  // Each Needs attention row draws its recent checks, fetched for the rows on
  // screen only: again whenever the list reloads, and when a row scrolls
  // into view with traces older than the current list.
  const { visible, observe, supported: observing } = useVisibleRows();
  // Which list load each row's trace was fetched for: a row is fetched once
  // per load, and a batch in flight is never cancelled (scrolling would
  // otherwise refetch the same rows over and over).
  const traceGeneration = useRef(new Map<string, DashboardProblemMonitor[]>());
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    if (!problemMonitorsData || sortedProblems.length === 0) return;
    const due = sortedProblems.filter(
      (m) => (!observing || visible.has(m.monitor_id)) && traceGeneration.current.get(m.monitor_id) !== problemMonitorsData,
    );
    if (due.length === 0) return;
    due.forEach((m) => traceGeneration.current.set(m.monitor_id, problemMonitorsData));
    void Promise.all(
      due.map(async (monitor) => {
        try {
          const response = await getMonitorResults(monitor.monitor_id, { limit: TRACE_SLOTS });
          return [monitor.monitor_id, response.results] as const;
        } catch {
          return [monitor.monitor_id, [] as CheckResult[]] as const;
        }
      }),
    ).then((entries) => {
      if (mounted.current) setTraces((current) => ({ ...current, ...Object.fromEntries(entries) }));
    });
  }, [sortedProblems, visible, observing, problemMonitorsData]);

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
    }).slice(0, TIMELINE_ROWS),
    [problemMonitors, recentAlerts, recentFailures, timeRange],
  );
  // A row that appears or changes event between refreshes flashes once.
  const timelineFlash = useRowFlash(timelineItems, (item) => item.id, (item) => `${item.event}|${item.occurredAt}`, {
    includeNew: true,
  });

  // The headline, spoken when a refresh changes it (not on first load).
  const [announcement, setAnnouncement] = useState('');
  const lastHeadline = useRef<string | null>(null);
  useEffect(() => {
    if (!dashboard) return;
    const headline = operationalSummary.label;
    if (lastHeadline.current !== null && lastHeadline.current !== headline) setAnnouncement(`Dashboard updated: ${headline}`);
    lastHeadline.current = headline;
  }, [dashboard, operationalSummary.label]);

  const selectedRangeDays = Math.floor((RANGES[timeRange].ms ?? 0) / 86_400_000);
  const showRetentionWarning =
    tenantRetentionDays !== null && tenantRetentionDays > 0 && selectedRangeDays > tenantRetentionDays;

  if (dashboard === null && !error) return <LoadingSkeleton />;

  return (
    <Page width="data">
      <p role="status" className="sr-only">
        {announcement}
      </p>
      <NotificationNudge unroutedMonitors={opsSummary?.unrouted_monitors ?? 0} />

      {error && (
        <Callout
          tone="danger"
          action={<Button variant="danger" size="xs" onClick={retry}>Retry</Button>}
        >
          {error}
        </Callout>
      )}

      <PageHeader
        title="Dashboard"
        titleAside={
          <LiveStatus
            updatedAt={live.updatedAt}
            refreshing={live.refreshing}
            failed={live.failed}
            onRefresh={retry}
            refreshLabel="Refresh dashboard"
          />
        }
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
        <div className="animate-fade-in">
          <OperationalSummaryCard summary={operationalSummary} />
        </div>
      )}

      {/* Without a summary there is nothing true to draw: the error callout
          above carries Retry, and empty panels would read as an all-clear. */}
      {dashboard && (
        <div className={`animate-fade-in ${GRID}`}>
          <div className={CELL_ATTENTION}>
            <NeedsAttentionPanel
              monitors={sortedProblems}
              traces={traces}
              total={attentionTotals.total}
              unhealthyCount={attentionTotals.unhealthy}
              downHref={downHref}
              loading={problemMonitorsData === null && !sectionsError}
              loadFailed={sectionsError && problemMonitorsData === null}
              onRetry={retry}
              noMatchingMonitors={noMatchingMonitors}
              rangeLabel={rangeLabel}
              observe={observe}
              flashClass={problemFlash.flashClass}
            />
          </div>
          <div className={CELL_CHART}>
            <UptimeResponseChart
              trendData={trendData}
              hasEnoughTrendData={hasEnoughTrendData}
              noMatchingMonitors={noMatchingMonitors}
              rangeLabel={rangeLabel}
            />
          </div>
          <div className={CELL_CHANGES}>
            <WhatChangedTimeline
              items={timelineItems}
              loading={(recentFailuresData === null || recentAlertsData === null) && !sectionsError}
              loadFailed={sectionsError && (recentFailuresData === null || recentAlertsData === null)}
              onRetry={retry}
              rangeLabel={rangeLabel}
              flashClass={timelineFlash.flashClass}
            />
          </div>
          <div className={CELL_GROUPS}>
            <ServiceGroupsPanel
              groupTags={dashboard.group_tags ?? []}
              groups={dashboard.groups ?? []}
              range={timeRange}
              filterTags={selectedTags}
              monitorStates={problemMonitors}
            />
          </div>
        </div>
      )}
    </Page>
  );
}
