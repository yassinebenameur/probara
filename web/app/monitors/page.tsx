'use client';

import PrometheusDetails from '@/components/monitors/PrometheusDetails';
import { createContext, Suspense, useCallback, useContext, useState, useEffect, useMemo, useRef, type CSSProperties, type MouseEvent } from 'react';
import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { Plus, Download, Upload, Search, Tag, ChevronDown, ChevronRight, Activity, ArrowUpDown, CheckSquare, FolderPlus, Move, Trash2, Bell, BellOff, Layers, X, Pencil, Copy, Pause, Play } from 'lucide-react';
import RowMenu, { RowMenuItem, RowMenuSeparator, useCloseRowMenu } from '@/components/ui/RowMenu';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import SharedSelectCheckbox from '@/components/ui/SelectCheckbox';
import FilterChip from '@/components/ui/FilterChip';
import PageHeader from '@/components/ui/PageHeader';
import SharedEmptyState from '@/components/ui/EmptyState';
import TableSkeleton from '@/components/ui/TableSkeleton';
import TypeBadge from '@/components/ui/TypeBadge';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import CopyableTarget from '@/components/ui/CopyableTarget';
import { useToast } from '@/components/ui/ToastProvider';
import { BulkAlertingModal } from '@/components/monitors/BulkAlertingModal';
import CheckTrace from '@/components/monitors/CheckTrace';
import TagPill from '@/components/monitors/TagPill';
import { Monitor, CheckResult, AgentMetrics, HTTPDisplayValue } from '@/lib/types';
import {
  deleteMonitor,
  getMonitorResults,
  getGroupMembers,
  createMonitor,
  addMonitorsToGroup,
  removeMonitorsFromGroup,
  getSyntheticBrowserScreenshotUrl,
  toggleMonitorEnabled,
  exportMonitors,
  snoozeMonitor,
} from '@/lib/api';
import {
  formatMonitorListParams,
  getAllMonitors,
  MONITOR_STATUS_FILTERS,
  MonitorListParams,
  MonitorListSort,
  MonitorStatusFilter,
  parseMonitorListParams,
  sortMonitorsByName,
} from '@/lib/monitor-list';
import { getApiKey } from '@/lib/auth';
import { useCanWrite, useIsTenantAdmin } from '@/components/providers/CurrentUserProvider';
import { isSipCall } from '@/lib/sip-config';
import {
  calculateUptime,
  countOperationalResults,
  displayValueLabel,
  formatInterval,
  getEffectiveMonitorStatus,
  getHTTPDisplayValues,
  isPlatformResult,
  getOperationalResults,
  monitorTarget,
  monitorTargetLabel,
  MonitorDisplayStatus,
  calculateLatencyStats,
  shortFailureReason,
  sortBySeverity,
} from '@/lib/monitor-utils';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';
import {
  formatDateTime,
  formatDateTimeFull,
  formatLatency,
  formatRelativeTime,
  formatUptime,
} from '@/lib/format';
import {
  CHECK_RESULT_BAR,
  MONITOR_STATUS_DOT,
  MONITOR_STATUS_LABEL,
  MONITOR_STATUS_TONE,
} from '@/lib/status-tones';

const DEBUG_INGEST_URL = process.env.NEXT_PUBLIC_DEBUG_INGEST_URL;
const NO_GROUP_VALUE = '__no_group__';

// The list fetches this many recent results per monitor. The trace, the
// Success column and the latency all describe this window, and the column
// headers say so.
const RECENT_CHECKS = 20;

type SortMode = MonitorListSort;
type StatusFilter = MonitorStatusFilter;

const STATUS_FILTER_LABEL: Record<StatusFilter, string> = {
  all: 'All',
  up: 'Up',
  down: 'Down',
  paused: 'Paused',
  maintenance: 'Maintenance',
};

function groupContainsGroup(
  rootGroupID: string,
  candidateDescendantID: string,
  groupMembersMap: Record<string, Monitor[]>
): boolean {
  const visited = new Set<string>();
  const stack = [rootGroupID];

  while (stack.length > 0) {
    const currentGroupID = stack.pop();
    if (!currentGroupID) continue;
    if (currentGroupID === candidateDescendantID) return true;
    if (visited.has(currentGroupID)) continue;

    visited.add(currentGroupID);
    const members = groupMembersMap[currentGroupID] || [];
    members.forEach((member) => {
      if (member.type === 'group' && !visited.has(member.id)) {
        stack.push(member.id);
      }
    });
  }

  return false;
}

function debugIngest(payload: Record<string, unknown>) {
  if (!DEBUG_INGEST_URL) return;
  fetch(DEBUG_INGEST_URL, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  }).catch(() => {});
}

// ── Row layout ───────────────────────────────────────────────────────────
//
// Header, monitor rows and group rows share one grid template, so every
// column lines up without spacer elements. The lead column holds the
// selection checkbox, the group chevron slot and the status dot; its width
// is the --row-lead variable the list sets. Below the wide breakpoint a row
// is name + compact trace + menu (--row-lead-narrow). The chevron keeps its
// slot at every width: it is the only way to reach members for Select mode,
// and a group expanded at lg must stay collapsible when the preview opens
// and the wide columns drop out. While the preview panel takes 344px of the width, the wide
// columns start at xl with trace and success only (the panel has the
// latency and the recent checks) and the full set returns at 2xl.
interface RowLayout {
  cols: string;
  header: string;
  /** Trace and success columns. */
  wide: string;
  /** Latency and last-check columns. */
  full: string;
  narrow: string;
}

const ROW_LAYOUTS: Record<'list' | 'panel', RowLayout> = {
  list: {
    cols: 'grid grid-cols-[var(--row-lead-narrow)_minmax(0,1fr)_auto_32px] lg:grid-cols-[var(--row-lead)_minmax(0,1fr)_minmax(120px,200px)_56px_64px_80px_32px]',
    header: 'hidden lg:grid',
    wide: 'hidden lg:flex',
    full: 'hidden lg:flex',
    narrow: 'flex lg:hidden',
  },
  panel: {
    cols: 'grid grid-cols-[var(--row-lead-narrow)_minmax(0,1fr)_auto_32px] xl:grid-cols-[var(--row-lead)_minmax(0,1fr)_minmax(100px,160px)_56px_32px] 2xl:grid-cols-[var(--row-lead)_minmax(0,1fr)_minmax(120px,200px)_56px_64px_80px_32px]',
    header: 'hidden xl:grid',
    wide: 'hidden xl:flex',
    full: 'hidden 2xl:flex',
    narrow: 'flex xl:hidden',
  },
};

const RowLayoutContext = createContext<RowLayout>(ROW_LAYOUTS.list);

function useRowLayout() {
  return useContext(RowLayoutContext);
}

/** Lead column width: [checkbox] [chevron slot] dot, 8px apart. */
function leadWidth(selectionMode: boolean, hasGroups: boolean): number {
  return 8 + (selectionMode ? 16 + 8 : 0) + (hasGroups ? 24 + 8 : 0);
}

function leadStyle(selectionMode: boolean, hasGroups: boolean): CSSProperties {
  return {
    '--row-lead': `${leadWidth(selectionMode, hasGroups)}px`,
    '--row-lead-narrow': `${leadWidth(selectionMode, hasGroups)}px`,
  } as CSSProperties;
}

// The previewed row carries an accent edge, checked rows a light accent
// tint. Keyboard focus lands on the row's name link and rings the whole row.
function rowStateClass(isSelected: boolean, isChecked: boolean) {
  const focus = 'has-[.row-link:focus-visible]:shadow-[inset_0_0_0_2px_rgba(var(--accent-rgb),0.55)]';
  if (isSelected) return `bg-well-hover shadow-[inset_2px_0_0_0_var(--accent)] ${focus}`;
  if (isChecked) return `bg-cyan-500/[0.06] ${focus}`;
  return `hover:bg-well ${focus}`;
}

// The name is a real link (focusable, opens in a new tab with a modifier),
// but a plain mouse click is left to the row: it previews on desktop and
// navigates below lg, the same as clicking anywhere else on the row.
// Keyboard activation (Enter fires a click with detail 0) navigates, as the
// link announces: the preview sits after every row in tab order and would
// open with no focus move or announcement.
function handleRowLinkClick(event: MouseEvent<HTMLAnchorElement>) {
  if (event.detail === 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) {
    event.stopPropagation();
    return;
  }
  event.preventDefault();
}

// Paused is a hollow ring so it never reads as "no data" (solid grey).
// `name` labels a dot that stands alone for a monitor (a group's member dots).
function StatusDot({ status, name }: { status: MonitorDisplayStatus; name?: string }) {
  const label = name ? `${name}: ${MONITOR_STATUS_LABEL[status]}` : MONITOR_STATUS_LABEL[status];
  const fill =
    status === 'paused' ? 'ring-[1.5px] ring-inset ring-slate-400' : MONITOR_STATUS_DOT[status];
  return <span role="img" aria-label={label} title={label} className={`h-2 w-2 flex-shrink-0 rounded-full ${fill}`} />;
}

/** Paused and maintenance are states the operator chose; say so on the row. */
function StatePill({ monitor, status }: { monitor: Monitor; status: MonitorDisplayStatus }) {
  if (status === 'paused') {
    return <Pill tone="neutral" size="xs">Paused</Pill>;
  }
  if (status === 'maintenance') {
    return (
      <span
        className="inline-flex flex-shrink-0"
        title={
          monitor.maintenance_until
            ? `Alerts suppressed until ${formatDateTime(monitor.maintenance_until)}`
            : 'Alerts suppressed during maintenance'
        }
      >
        <Pill tone="info" size="xs">Maintenance</Pill>
      </span>
    );
  }
  return null;
}

/**
 * Second line of a row: the type tag, then the target. Below md the target is
 * plain text (the row opens the detail page, which has copy); from md it is
 * a copy chip whose actions appear on hover. Targetless types show their
 * interval so every row keeps the same height.
 */
function TargetLine({ monitor }: { monitor: Monitor }) {
  const target = monitorTarget(monitor);
  if (!target) {
    return <span className="truncate text-xs text-slate-500">{formatInterval(monitor.interval_seconds)}</span>;
  }
  return (
    <>
      <span className="min-w-0 truncate font-mono text-xs text-slate-500 md:hidden">{target}</span>
      <span className="hidden min-w-0 md:flex">
        <CopyableTarget
          value={target}
          label={monitorTargetLabel(monitor.type)}
          size="sm"
          revealOnHover
          textClassName="text-slate-500"
        />
      </span>
    </>
  );
}

function TagList({ tags, max }: { tags?: string[]; max: number }) {
  if (!tags || tags.length === 0) return null;
  return (
    <span className="hidden flex-shrink-0 items-center gap-1 sm:flex">
      {tags.slice(0, max).map((tag) => (
        <TagPill key={tag} tag={tag} size="xs" />
      ))}
      {tags.length > max && <span className="text-2xs text-slate-500">+{tags.length - max}</span>}
    </span>
  );
}

/**
 * Trace, success share, latency and last check: the wide columns, plus the
 * compact cell (short trace + success) that replaces them on narrow screens.
 */
function RowMetrics({ results, showLatency = true }: { results: CheckResult[]; showLatency?: boolean }) {
  const layout = useRowLayout();
  const operational = getOperationalResults(results);
  const passed = operational.filter((r) => r.status === 'success').length;
  const success = operational.length > 0 ? formatUptime(calculateUptime(results)) : '—';
  const successTitle =
    operational.length > 0 ? `${passed} of the last ${operational.length} checks passed` : 'No checks yet';
  const latency = calculateLatencyStats(results);
  const latest = results[0];

  return (
    <>
      <div className={`${layout.narrow} flex-col items-end gap-1`}>
        <CheckTrace results={results} slots={10} size="sm" />
        <span className="text-2xs tabular-nums text-slate-400" title={successTitle}>{success}</span>
      </div>
      <div className={`${layout.wide} min-w-0 justify-end`}>
        <CheckTrace results={results} slots={RECENT_CHECKS} className="w-full" />
      </div>
      <div className={`${layout.wide} justify-end text-xs tabular-nums text-slate-300`} title={successTitle}>
        {success}
      </div>
      <div className={`${layout.full} justify-end text-xs tabular-nums text-slate-400`}>
        {showLatency && latency.samples > 0 ? formatLatency(latency.median) : '—'}
      </div>
      <div
        className={`${layout.full} justify-end whitespace-nowrap text-xs tabular-nums text-slate-500`}
        title={latest ? formatDateTimeFull(latest.created_at) : undefined}
      >
        {latest ? formatRelativeTime(latest.created_at) : 'Never'}
      </div>
    </>
  );
}

// Format bytes to human readable
function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
}

// Agent metrics mini display
function AgentMetricsMini({ metrics }: { metrics: AgentMetrics }) {
  return (
    <span className="hidden flex-shrink-0 items-center gap-3 text-2xs text-slate-500 sm:flex">
      <span>
        CPU <span className="font-medium tabular-nums text-slate-300">{metrics.cpu_percent.toFixed(0)}%</span>
      </span>
      <span>
        Mem{' '}
        <span className="font-medium tabular-nums text-slate-300">
          {((metrics.memory_used / metrics.memory_total) * 100).toFixed(0)}%
        </span>
      </span>
      <span>
        Disk{' '}
        <span className="font-medium tabular-nums text-slate-300">
          {((metrics.disk_used / metrics.disk_total) * 100).toFixed(0)}%
        </span>
      </span>
    </span>
  );
}

function isAgentMetrics(metrics: unknown): metrics is AgentMetrics {
  if (!metrics || typeof metrics !== 'object') return false;
  const candidate = metrics as AgentMetrics;
  return (
    typeof candidate.cpu_percent === 'number' &&
    typeof candidate.memory_used === 'number' &&
    typeof candidate.memory_total === 'number' &&
    typeof candidate.disk_used === 'number' &&
    typeof candidate.disk_total === 'number' &&
    typeof candidate.load_avg_1 === 'number'
  );
}

// Values the worker read from the latest JSON response (HTTP display_fields).
// Neutral and squared-off on purpose, so they never read as tags. Shown at
// every width (the list is the only place they appear outside the preview),
// shrinking with the name on phones.
function DisplayValueChips({ values }: { values: HTTPDisplayValue[] }) {
  if (values.length === 0) return null;
  return (
    <span className="flex min-w-0 items-center gap-1 overflow-hidden">
      {values.map((v) => (
        <span
          key={v.path}
          className="inline-flex min-w-0 max-w-[120px] flex-shrink-0 items-center sm:max-w-[180px] gap-1 rounded-md bg-well-hover px-1.5 py-0.5 font-mono text-2xs leading-none text-slate-300"
          title={`${v.path} = ${v.value}`}
        >
          {displayValueLabel(v) && <span className="shrink-0 text-slate-500">{displayValueLabel(v)}</span>}
          <span className="truncate">{v.value}</span>
        </span>
      ))}
    </span>
  );
}

function SelectCheckbox({
  checked,
  onChange,
  visible = true,
  label,
}: {
  checked: boolean;
  onChange: () => void;
  visible?: boolean;
  label?: string;
}) {
  if (!visible) return null;
  return <SharedSelectCheckbox checked={checked} onChange={onChange} label={label} />;
}

function SnoozeOptions({ onSnooze }: { onSnooze: (minutes: number) => void }) {
  const close = useCloseRowMenu();
  return (
    <>
      <div className="px-2.5 pt-1 text-xs text-slate-500">Snooze alerts</div>
      <div className="flex gap-1 px-1.5 pb-1 pt-1.5">
        {[
          { label: '1h', minutes: 60 },
          { label: '4h', minutes: 240 },
          { label: '24h', minutes: 1440 },
        ].map(({ label, minutes }) => (
          <Button
            key={label}
            type="button"
            variant="ghost"
            size="xs"
            className="flex-1 !px-0"
            onClick={() => {
              close();
              onSnooze(minutes);
            }}
          >
            {label}
          </Button>
        ))}
      </div>
    </>
  );
}

function MonitorActionsMenu({
  monitor,
  onToggleEnabled,
  monitorId,
  onDelete,
  onSnooze,
}: {
  monitor: Monitor;
  onToggleEnabled: (monitor: Monitor) => void;
  monitorId: string;
  onDelete: () => void;
  onSnooze: (monitor: Monitor, durationMinutes: number) => void;
}) {
  // Cloning, pausing or resuming a SIP test call is a tenant admin's: the
  // API refuses anyone else, since each check places a real call.
  const isTenantAdmin = useIsTenantAdmin();
  const callLocked = isSipCall(monitor.type, monitor.config) && !isTenantAdmin;
  return (
    <RowMenu label={`Actions for ${monitor.name}`}>
      <RowMenuItem href={`/monitors/${monitorId}`} icon={<Pencil strokeWidth={1.75} />}>Edit</RowMenuItem>
      {!callLocked && (
        <RowMenuItem href={`/monitors/new?clone=${monitorId}`} icon={<Copy strokeWidth={1.75} />}>Clone</RowMenuItem>
      )}
      {!callLocked && (
        <RowMenuItem
          onSelect={() => onToggleEnabled(monitor)}
          icon={monitor.enabled ? <Pause strokeWidth={1.75} /> : <Play strokeWidth={1.75} />}
        >
          {monitor.enabled ? 'Pause' : 'Resume'}
        </RowMenuItem>
      )}
      <RowMenuSeparator />
      <SnoozeOptions onSnooze={(minutes) => onSnooze(monitor, minutes)} />
      <RowMenuSeparator />
      <RowMenuItem onSelect={onDelete} icon={<Trash2 strokeWidth={1.75} />} danger>Delete</RowMenuItem>
    </RowMenu>
  );
}

// The ⋮ cell. Hover-revealed on pointer layouts, always shown on touch.
function RowActions(props: Parameters<typeof MonitorActionsMenu>[0]) {
  return (
    <div className="flex justify-end opacity-100 transition-opacity focus-within:opacity-100 md:opacity-0 md:focus-within:opacity-100 md:group-hover:opacity-100 md:group-has-[.row-link:focus-visible]:opacity-100">
      <MonitorActionsMenu {...props} />
    </div>
  );
}

// Compact monitor row component
function MonitorRow({
  monitor,
  results,
  isSelected,
  onClick,
  onDelete,
  onToggleEnabled,
  onSnooze,
  isChecked,
  onToggleSelect,
  selectionMode,
}: {
  monitor: Monitor;
  results: CheckResult[];
  isSelected: boolean;
  onClick: () => void;
  onDelete: () => void;
  onToggleEnabled: (monitor: Monitor) => void;
  onSnooze: (monitor: Monitor, durationMinutes: number) => void;
  isChecked: boolean;
  onToggleSelect: () => void;
  selectionMode: boolean;
}) {
  const layout = useRowLayout();
  const status = getEffectiveMonitorStatus(monitor, results);
  const latestResult = results[0];
  const agentMetrics = isAgentMetrics(latestResult?.metrics_data) ? latestResult.metrics_data : null;
  const displayValues = getHTTPDisplayValues(results);

  return (
    <div
      onClick={onClick}
      className={`group ${layout.cols} cursor-pointer items-center gap-x-3 px-4 py-2 transition-colors ${rowStateClass(isSelected, isChecked)}`}
    >
      {/* Lead: [checkbox] [chevron slot] dot */}
      <div className="flex items-center justify-end gap-2">
        <SelectCheckbox
          checked={isChecked}
          onChange={onToggleSelect}
          visible={selectionMode}
          label={`Select ${monitor.name}`}
        />
        <StatusDot status={status} />
      </div>

      <div className="min-w-0">
        <div className="flex min-w-0 items-center gap-2 overflow-hidden">
          <Link
            href={`/monitors/${monitor.id}`}
            onClick={handleRowLinkClick}
            className="row-link min-w-0 truncate text-sm font-medium text-white focus-visible:outline-none"
          >
            {monitor.name}
          </Link>
          <StatePill monitor={monitor} status={status} />
          <DisplayValueChips values={displayValues} />
          <TagList tags={monitor.tags} max={3} />
        </div>
        <div className="mt-1 flex min-w-0 items-center gap-2">
          <TypeBadge type={monitor.type} config={monitor.config} />
          <TargetLine monitor={monitor} />
          {monitor.type === 'agent' && agentMetrics && <AgentMetricsMini metrics={agentMetrics} />}
        </div>
      </div>

      <RowMetrics results={results} />

      <RowActions
        monitor={monitor}
        monitorId={monitor.id}
        onToggleEnabled={onToggleEnabled}
        onSnooze={onSnooze}
        onDelete={onDelete}
      />
    </div>
  );
}

// Group card with expandable children
function GroupCard({
  monitor,
  results,
  members,
  groupMembersMap,
  memberResults,
  isSelected,
  onClick,
  onDelete,
  onToggleEnabled,
  onSnooze,
  isChecked,
  onToggleSelect,
  onToggleMonitorSelection,
  onDeleteMonitor,
  selectionMode,
  onSelectMonitor,
  selectedMonitorId,
  selectedMonitorIds,
  orderMembers,
  visitedGroupIds = new Set<string>(),
  autoExpand = false,
  memberMatcher,
  groupHasMatch,
}: {
  monitor: Monitor;
  results: CheckResult[];
  members: Monitor[];
  groupMembersMap: Record<string, Monitor[]>;
  memberResults: Record<string, CheckResult[]>;
  isSelected: boolean;
  onClick: () => void;
  onDelete: () => void;
  onToggleEnabled: (monitor: Monitor) => void;
  onSnooze: (monitor: Monitor, durationMinutes: number) => void;
  isChecked: boolean;
  onToggleSelect: () => void;
  onToggleMonitorSelection: (id: string) => void;
  onDeleteMonitor: (id: string) => void;
  selectionMode: boolean;
  onSelectMonitor: (id: string) => void;
  selectedMonitorId: string | null;
  selectedMonitorIds: Set<string>;
  /** The list's sort, applied to members too. */
  orderMembers: (members: Monitor[]) => Monitor[];
  visitedGroupIds?: Set<string>;
  autoExpand?: boolean;
  memberMatcher?: (monitor: Monitor) => boolean;
  groupHasMatch?: (groupId: string) => boolean;
}) {
  const layout = useRowLayout();
  const [expanded, setExpanded] = useState(false); // Collapsed by default

  // Expand automatically while a filter matches members inside this group,
  // and collapse again when that filter is cleared (unless the user expanded it).
  const wasAutoExpanded = useRef(false);
  useEffect(() => {
    if (autoExpand) {
      setExpanded((prev) => {
        if (!prev) wasAutoExpanded.current = true;
        return true;
      });
    } else if (wasAutoExpanded.current) {
      wasAutoExpanded.current = false;
      setExpanded(false);
    }
  }, [autoExpand]);
  const status = getEffectiveMonitorStatus(monitor, results);
  const nextVisitedGroupIDs = new Set(visitedGroupIds);
  nextVisitedGroupIDs.add(monitor.id);

  // Calculate aggregate stats from members
  const memberStatuses = members.map((m) => getEffectiveMonitorStatus(m, memberResults[m.id] || []));
  const healthyCount = memberStatuses.filter((s) => s === 'up').length;
  const downCount = memberStatuses.filter((s) => s === 'down' || s === 'degraded').length;
  const healthTone = downCount > 0
    ? 'text-rose-300'
    : healthyCount === members.length && members.length > 0
      ? 'text-emerald-300'
      : 'text-slate-500';

  // While filtering, only show the members that match (fall back to all if
  // the group itself matched but none of its members did).
  const matchingMembers = memberMatcher
    ? members.filter(
        (member) =>
          memberMatcher(member) ||
          (member.type === 'group' && groupHasMatch?.(member.id))
      )
    : members;
  const visibleMembers = orderMembers(matchingMembers.length > 0 ? matchingMembers : members);
  const hiddenMemberCount = members.length - visibleMembers.length;

  return (
    <div>
      {/* Group Header */}
      <div
        onClick={onClick}
        className={`group ${layout.cols} cursor-pointer items-center gap-x-3 px-4 py-2 transition-colors ${rowStateClass(isSelected, isChecked)}`}
      >
        <div className="flex items-center justify-end gap-2">
          <SelectCheckbox
            checked={isChecked}
            onChange={onToggleSelect}
            visible={selectionMode}
            label={`Select ${monitor.name}`}
          />
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); setExpanded(!expanded); }}
            aria-expanded={expanded}
            aria-label={`${expanded ? 'Collapse' : 'Expand'} ${monitor.name}`}
            className={`flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md text-slate-400 transition-colors hover:bg-well-hover hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50`}
          >
            <ChevronRight
              className={`h-4 w-4 transition-transform ${expanded ? 'rotate-90' : ''}`}
              strokeWidth={1.75}
            />
          </button>
          <StatusDot status={status} />
        </div>

        <div className="min-w-0">
          <div className="flex min-w-0 items-center gap-2 overflow-hidden">
            <Link
              href={`/monitors/${monitor.id}`}
              onClick={handleRowLinkClick}
              className="row-link min-w-0 truncate text-sm font-medium text-white focus-visible:outline-none"
            >
              {monitor.name}
            </Link>
            <StatePill monitor={monitor} status={status} />
            <TagList tags={monitor.tags} max={2} />
          </div>
          <div className="mt-1 flex min-w-0 items-center gap-2">
            <TypeBadge type="group" />
            <span className={`whitespace-nowrap text-xs tabular-nums ${healthTone}`}>
              {healthyCount}/{members.length} healthy
            </span>
            {/* Member dots: which members, at a glance. */}
            <span className="hidden min-w-0 items-center gap-1 overflow-hidden sm:flex">
              {members.slice(0, 8).map((m, i) => (
                <StatusDot key={m.id} status={memberStatuses[i]} name={m.name} />
              ))}
              {members.length > 8 && <span className="text-2xs text-slate-500">+{members.length - 8}</span>}
            </span>
          </div>
        </div>

        {/* A group's own checks are its roll-up; latency means nothing there. */}
        <RowMetrics results={results} showLatency={false} />

        <RowActions
          monitor={monitor}
          monitorId={monitor.id}
          onToggleEnabled={onToggleEnabled}
          onSnooze={onSnooze}
          onDelete={onDelete}
        />
      </div>

      {/* Expanded Children */}
      {expanded && visibleMembers.length > 0 && (
        <div className="dashboard-scroll max-h-[60vh] overflow-y-auto overscroll-contain border-t border-divider bg-well pl-6 ![scrollbar-gutter:auto]">
          {hiddenMemberCount > 0 && (
            <p className="px-4 pb-1 pt-2.5 text-xs text-slate-500">
              Showing {visibleMembers.length} matching of {members.length} members
            </p>
          )}
          <div className="divide-y divide-divider">
          {visibleMembers.map((member) => {
            const memberKey = `${monitor.id}:${member.id}`;
            if (member.type === 'group') {
              const cycleDetected = nextVisitedGroupIDs.has(member.id);
              return (
                <div key={memberKey}>
                  {cycleDetected ? (
                    <>
                      <MonitorRow
                        monitor={member}
                        results={memberResults[member.id] || []}
                        isSelected={member.id === selectedMonitorId}
                        onClick={() => onSelectMonitor(member.id)}
                        onDelete={() => onDeleteMonitor(member.id)}
                        onToggleEnabled={onToggleEnabled}
                        onSnooze={onSnooze}
                        isChecked={selectedMonitorIds.has(member.id)}
                        onToggleSelect={() => onToggleMonitorSelection(member.id)}
                        selectionMode={selectionMode}
                      />
                      <p className="px-4 pb-2.5 text-xs text-rose-300">
                        Cycle detected in group nesting. Expansion stopped.
                      </p>
                    </>
                  ) : (
                    <GroupCard
                      monitor={member}
                      results={memberResults[member.id] || []}
                      members={groupMembersMap[member.id] || []}
                      groupMembersMap={groupMembersMap}
                      memberResults={memberResults}
                      isSelected={member.id === selectedMonitorId}
                      onClick={() => onSelectMonitor(member.id)}
                      onDelete={() => onDeleteMonitor(member.id)}
                      onToggleEnabled={onToggleEnabled}
                      onSnooze={onSnooze}
                      isChecked={selectedMonitorIds.has(member.id)}
                      onToggleSelect={() => onToggleMonitorSelection(member.id)}
                      onToggleMonitorSelection={onToggleMonitorSelection}
                      onDeleteMonitor={onDeleteMonitor}
                      selectionMode={selectionMode}
                      onSelectMonitor={onSelectMonitor}
                      selectedMonitorId={selectedMonitorId}
                      selectedMonitorIds={selectedMonitorIds}
                      orderMembers={orderMembers}
                      visitedGroupIds={nextVisitedGroupIDs}
                      autoExpand={Boolean(memberMatcher) && groupHasMatch?.(member.id)}
                      memberMatcher={memberMatcher}
                      groupHasMatch={groupHasMatch}
                    />
                  )}
                </div>
              );
            }

            return (
              <MonitorRow
                key={memberKey}
                monitor={member}
                results={memberResults[member.id] || []}
                isSelected={member.id === selectedMonitorId}
                onClick={() => onSelectMonitor(member.id)}
                onDelete={() => onDeleteMonitor(member.id)}
                onToggleEnabled={onToggleEnabled}
                onSnooze={onSnooze}
                isChecked={selectedMonitorIds.has(member.id)}
                onToggleSelect={() => onToggleMonitorSelection(member.id)}
                selectionMode={selectionMode}
              />
            );
          })}
          </div>
        </div>
      )}
    </div>
  );
}

// Detail Panel Component
function DetailPanel({
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
  const [screenshotBlobURL, setScreenshotBlobURL] = useState<string | null>(null);
  const [screenshotLoading, setScreenshotLoading] = useState(false);

  const getSyntheticBrowserScreenshotPath = (result?: CheckResult): string | null => {
    const md = result?.metrics_data as any;
    return md?.synthetic_browser?.artifacts?.screenshot_path || null;
  };

  const latestSyntheticBrowserResult = monitor?.type === 'synthetic_browser'
    ? results.find((result) => Boolean(getSyntheticBrowserScreenshotPath(result)))
    : undefined;
  const latestSyntheticBrowserScreenshotPath = getSyntheticBrowserScreenshotPath(latestSyntheticBrowserResult);
  const syntheticBrowserScreenshotURL = monitor && latestSyntheticBrowserScreenshotPath
    ? getSyntheticBrowserScreenshotUrl(monitor.id, monitor.tenant_id, latestSyntheticBrowserScreenshotPath)
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
  const agentMetrics = isAgentMetrics(latestResult?.metrics_data) ? latestResult.metrics_data : null;
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
  const meterTrack = 'mt-2 h-1 overflow-hidden rounded-full bg-well-hover';

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
                      className="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-well focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50"
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

        {/* Agent Metrics Section */}
        {monitor.type === 'agent' && agentMetrics && (
          <div>
            <h4 className={sectionTitle}>System metrics</h4>
            <div className="grid grid-cols-2 gap-2">
              <div className="glass-well p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs text-slate-400">CPU</span>
                  <span className="text-xs font-medium tabular-nums text-white">{agentMetrics.cpu_percent.toFixed(1)}%</span>
                </div>
                <div className={meterTrack}>
                  <div className="h-full rounded-full bg-sky-500" style={{ width: `${agentMetrics.cpu_percent}%` }} />
                </div>
              </div>
              <div className="glass-well p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs text-slate-400">Memory</span>
                  <span className="text-xs font-medium tabular-nums text-white">
                    {((agentMetrics.memory_used / agentMetrics.memory_total) * 100).toFixed(1)}%
                  </span>
                </div>
                <div className={meterTrack}>
                  <div className="h-full rounded-full bg-sky-500" style={{ width: `${(agentMetrics.memory_used / agentMetrics.memory_total) * 100}%` }} />
                </div>
              </div>
              <div className="glass-well p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs text-slate-400">Disk</span>
                  <span className="text-xs font-medium tabular-nums text-white">
                    {((agentMetrics.disk_used / agentMetrics.disk_total) * 100).toFixed(1)}%
                  </span>
                </div>
                <div className={meterTrack}>
                  <div className="h-full rounded-full bg-sky-500" style={{ width: `${(agentMetrics.disk_used / agentMetrics.disk_total) * 100}%` }} />
                </div>
              </div>
              <div className="glass-well p-3">
                <div className="flex items-center justify-between">
                  <span className="text-xs text-slate-400">Load avg</span>
                  <span className="text-xs font-medium tabular-nums text-white">{agentMetrics.load_avg_1.toFixed(2)}</span>
                </div>
                <p className="mt-1.5 text-2xs text-slate-500">{agentMetrics.process_count} processes</p>
              </div>
            </div>
            <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-xs">
              <div className="flex justify-between text-slate-500">
                <span>Network in</span>
                <span className="tabular-nums text-slate-300">{formatBytes(agentMetrics.network_bytes_in)}</span>
              </div>
              <div className="flex justify-between text-slate-500">
                <span>Network out</span>
                <span className="tabular-nums text-slate-300">{formatBytes(agentMetrics.network_bytes_out)}</span>
              </div>
            </div>
          </div>
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
                  {latestSyntheticBrowserResult ? formatRelativeTime(latestSyntheticBrowserResult.created_at) : 'Latest run'}
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
                      <span className="whitespace-nowrap text-slate-500" title={formatDateTimeFull(r.created_at)}>
                        {formatRelativeTime(r.created_at)}
                      </span>
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

// Column labels on the rows' grid. Only shown where the wide columns are.
function sortToggleLabel(sort: SortMode): string {
  return `Sorted by ${sort === 'status' ? 'status, then name' : 'name'}. Switch to ${sort === 'status' ? 'name' : 'status'}`;
}

// The sort switch where the column header is hidden (narrow layouts, or the
// preview panel taking the width): the header holds it everywhere else.
function NarrowSortChip({ sort, onSortChange }: { sort: SortMode; onSortChange: (sort: SortMode) => void }) {
  const layout = useRowLayout();
  return (
    <FilterChip
      // `narrow` is "flex …:hidden"; the chip keeps its own inline-flex.
      className={layout.narrow.replace(/^flex /, '')}
      icon={<ArrowUpDown strokeWidth={1.75} />}
      onClick={() => onSortChange(sort === 'status' ? 'name' : 'status')}
      aria-pressed={undefined}
      aria-label={sortToggleLabel(sort)}
      title={sortToggleLabel(sort)}
    >
      {sort === 'status' ? 'Status, then name' : 'Name'}
    </FilterChip>
  );
}

// Reads the list filters from the URL on every navigation, including
// same-route ones (sidebar link to /monitors, back/forward), which keep this
// page mounted. useSearchParams needs a Suspense boundary for the prerender,
// so it lives in this leaf rather than in the page.
function MonitorListUrlSync({ onParams }: { onParams: (params: MonitorListParams) => void }) {
  const searchParams = useSearchParams();
  const search = searchParams.toString();
  useEffect(() => {
    onParams(parseMonitorListParams(search));
  }, [search, onParams]);
  return null;
}

function ListHeader({ sort, onSortChange }: { sort: SortMode; onSortChange: (sort: SortMode) => void }) {
  const layout = useRowLayout();
  return (
    <div className={`${layout.header} ${layout.cols} items-center gap-x-3 border-b border-divider px-4 py-2 text-xs font-medium text-slate-500`}>
      <span />
      <span className="min-w-0">
        <button
          type="button"
          onClick={() => onSortChange(sort === 'status' ? 'name' : 'status')}
          aria-label={sortToggleLabel(sort)}
          title={sortToggleLabel(sort)}
          className="-mx-1 inline-flex max-w-full items-center gap-1 whitespace-nowrap rounded px-1 transition-colors hover:text-white focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/50"
        >
          {sort === 'status' ? 'Status, then name' : 'Name'}
          <ArrowUpDown className="h-3 w-3" strokeWidth={1.75} aria-hidden="true" />
        </button>
      </span>
      <span className="whitespace-nowrap text-right" title="One bar per check, newest on the right">Last {RECENT_CHECKS} checks</span>
      <span className="text-right" title={`Share of the last ${RECENT_CHECKS} checks that passed`}>Success</span>
      <span className={`${layout.full} justify-end`} title={`Median of the successful checks among the last ${RECENT_CHECKS}`}>Latency</span>
      <span className={`${layout.full} justify-end whitespace-nowrap`}>Last check</span>
      <span />
    </div>
  );
}

export default function MonitorsPage() {
  const canWrite = useCanWrite();
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>('');
  const [searchTerm, setSearchTerm] = useState('');
  const [selectedTypes, setSelectedTypes] = useState<Set<string>>(new Set());
  const [showTypeFilter, setShowTypeFilter] = useState(false);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  // Deep-linked from the dashboard's unrouted warning (?routing=unrouted).
  const [unroutedOnly, setUnroutedOnly] = useState(false);
  const [sortMode, setSortMode] = useState<SortMode>('status');
  const [selectedTags, setSelectedTags] = useState<Set<string>>(new Set());
  const [showTagFilter, setShowTagFilter] = useState(false);

  // Filters live in the URL so other pages can deep-link them (the
  // dashboard's ?status=down, ?tag=…, ?routing=unrouted) and a reload keeps
  // them. MonitorListUrlSync applies the URL on every navigation; state
  // changes are written back with replaceState, which Next mirrors into
  // useSearchParams, so the re-read then finds nothing to change.
  const [urlRead, setUrlRead] = useState(false);
  const applyUrlParams = useCallback((params: MonitorListParams) => {
    setStatusFilter(params.status);
    setUnroutedOnly(params.unroutedOnly);
    setSortMode(params.sort);
    setSelectedTags((current) =>
      current.size === params.tags.length && params.tags.every((tag) => current.has(tag))
        ? current
        : new Set(params.tags)
    );
    if (params.tags.length > 0) setShowTagFilter(true);
    setUrlRead(true);
  }, []);

  useEffect(() => {
    if (!urlRead) return;
    const search = formatMonitorListParams(
      { status: statusFilter, tags: Array.from(selectedTags), unroutedOnly, sort: sortMode },
      window.location.search
    );
    if (search !== window.location.search.replace(/^\?/, '')) {
      window.history.replaceState(null, '', `${window.location.pathname}${search ? `?${search}` : ''}${window.location.hash}`);
    }
  }, [urlRead, statusFilter, selectedTags, unroutedOnly, sortMode]);
  const [selectedMonitorId, setSelectedMonitorId] = useState<string | null>(null);
  const [checkResultsMap, setCheckResultsMap] = useState<Record<string, CheckResult[]>>({});
  // False until the first full results pass lands. Until then a monitor
  // without results reads as "unknown" and would sort above "up", so rows
  // keep name order and re-rank once instead of jumping with every chunk.
  const [resultsReady, setResultsReady] = useState(false);
  const [groupMembersMap, setGroupMembersMap] = useState<Record<string, Monitor[]>>({});
  const { showToast } = useToast();
  const [pendingDelete, setPendingDelete] = useState<Monitor | null>(null);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [selectionMode, setSelectionMode] = useState(false);
  const [selectedMonitorIds, setSelectedMonitorIds] = useState<Set<string>>(new Set());
  const [groupNameInput, setGroupNameInput] = useState('');
  const [targetGroupId, setTargetGroupId] = useState('');
  const [exporting, setExporting] = useState(false);
  const [showBulkAlerting, setShowBulkAlerting] = useState(false);
  const router = useRouter();

  // Below lg the detail panel is not rendered, so selecting a row would do
  // nothing visible — open the monitor's page instead.
  const selectMonitor = (id: string) => {
    if (selectionMode) {
      setSelectedMonitorId(id);
      return;
    }
    if (typeof window !== 'undefined' && !window.matchMedia('(min-width: 1024px)').matches) {
      router.push(`/monitors/${id}`);
      return;
    }
    // The preview panel is opt-in: clicking the previewed row again closes it.
    setSelectedMonitorId((current) => (current === id ? null : id));
  };

  useEffect(() => {
    loadMonitors();
  }, []);

  const loadMonitors = async () => {
    // #region agent log
    const loadStartTime = Date.now();
    debugIngest({
      location: 'page.tsx:loadMonitors:start',
      message: 'loadMonitors started (progressive)',
      data: {},
      timestamp: Date.now(),
      sessionId: 'debug-session',
      runId: 'progressive',
      hypothesisId: 'A',
    });
    // #endregion
    try {
      setLoading(true);
      setError('');
      // #region agent log
      const getMonitorsStart = Date.now();
      // #endregion
      const monitorsList = await getAllMonitors();
      // #region agent log
      debugIngest({
        location: 'page.tsx:loadMonitors:getMonitors',
        message: 'getMonitors API call completed',
        data: {
          durationMs: Date.now() - getMonitorsStart,
          monitorCount: monitorsList.length,
        },
        timestamp: Date.now(),
        sessionId: 'debug-session',
        runId: 'progressive',
        hypothesisId: 'A',
      });
      // #endregion
      setMonitors(monitorsList);

      // FIX: Progressive loading - show UI immediately, load results in background
      // Stop showing loading spinner now - monitors list is ready
      setLoading(false);

      // Fetch check results and group members progressively in chunks
      const resultsMap: Record<string, CheckResult[]> = {};
      const membersMap: Record<string, Monitor[]> = {};

      // #region agent log
      const resultsStartTime = Date.now();
      let apiCallCount = 0;
      // #endregion

      // FIX: Process in chunks of 6 (browser connection limit) to avoid queuing
      const CHUNK_SIZE = 6;
      for (let i = 0; i < monitorsList.length; i += CHUNK_SIZE) {
        const chunk = monitorsList.slice(i, i + CHUNK_SIZE);
        await Promise.all(
          chunk.map(async (monitor) => {
            try {
              // #region agent log
              apiCallCount++;
              const resultStart = Date.now();
              // #endregion
              const results = await getMonitorResults(monitor.id, { limit: RECENT_CHECKS });
              // #region agent log
              debugIngest({
                location: 'page.tsx:loadMonitors:getMonitorResults',
                message: 'getMonitorResults for single monitor',
                data: {
                  monitorId: monitor.id,
                  durationMs: Date.now() - resultStart,
                  resultsCount: results?.results?.length || 0,
                },
                timestamp: Date.now(),
                sessionId: 'debug-session',
                runId: 'progressive',
                hypothesisId: 'A,B',
              });
              // #endregion
              resultsMap[monitor.id] = results.results || [];

              if (monitor.type === 'group') {
                try {
                  // #region agent log
                  apiCallCount++;
                  const membersStart = Date.now();
                  // #endregion
                  const members = sortMonitorsByName(await getGroupMembers(monitor.id));
                  // #region agent log
                  debugIngest({
                    location: 'page.tsx:loadMonitors:getGroupMembers',
                    message: 'getGroupMembers call',
                    data: {
                      groupId: monitor.id,
                      durationMs: Date.now() - membersStart,
                      memberCount: members?.length || 0,
                    },
                    timestamp: Date.now(),
                    sessionId: 'debug-session',
                    runId: 'progressive',
                    hypothesisId: 'C',
                  });
                  // #endregion
                  membersMap[monitor.id] = members;
                  members.forEach((m) => {
                    if (!resultsMap[m.id]) {
                      resultsMap[m.id] = [];
                    }
                  });
                } catch { membersMap[monitor.id] = []; }
              }
            } catch {
              resultsMap[monitor.id] = [];
            }
          })
        );
        // Update state after each chunk so UI updates progressively. Merge
        // over the previous maps so a refetch never blanks rows that are
        // already on screen; the exact maps replace them once all chunks land.
        setCheckResultsMap((prev) => ({ ...prev, ...resultsMap }));
        setGroupMembersMap((prev) => ({ ...prev, ...membersMap }));
      }
      setCheckResultsMap({ ...resultsMap });
      setGroupMembersMap({ ...membersMap });
      setResultsReady(true);

      // #region agent log
      debugIngest({
        location: 'page.tsx:loadMonitors:allResultsFetched',
        message: 'All results and members fetched (progressive)',
        data: {
          totalApiCalls: apiCallCount,
          totalResultsFetchDurationMs: Date.now() - resultsStartTime,
        },
        timestamp: Date.now(),
        sessionId: 'debug-session',
        runId: 'progressive',
        hypothesisId: 'A,D',
      });
      // #endregion
    } catch (err: any) {
      setError(err.message || 'Failed to load monitors');
      setMonitors([]);
      setLoading(false);
      setResultsReady(true);
    }
    // #region agent log
    debugIngest({
      location: 'page.tsx:loadMonitors:end',
      message: 'loadMonitors completed (progressive)',
      data: { totalDurationMs: Date.now() - loadStartTime },
      timestamp: Date.now(),
      sessionId: 'debug-session',
      runId: 'progressive',
      hypothesisId: 'A',
    });
    // #endregion
  };

  const handleExport = async () => {
    try {
      setExporting(true);
      const { blob, filename } = await exportMonitors();
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = filename;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      showToast('Monitor export downloaded', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to export monitors', 'error');
    } finally {
      setExporting(false);
    }
  };

  const requestDeleteById = (id: string) => {
    const all = [...monitors, ...Object.values(groupMembersMap).flat()];
    const target = all.find((m) => m.id === id);
    if (target) setPendingDelete(target);
  };

  const handleDelete = async () => {
    if (!pendingDelete) return;
    const id = pendingDelete.id;
    setDeleting(true);
    try {
      await deleteMonitor(id);
      setMonitors(monitors.filter((m) => m.id !== id));
      if (selectedMonitorId === id) {
        setSelectedMonitorId(null);
      }
      showToast('Monitor deleted', 'success');
      setPendingDelete(null);
    } catch (err: any) {
      showToast(err.message || 'Failed to delete', 'error');
    } finally {
      setDeleting(false);
    }
  };

  const handleToggleEnabled = async (monitor: Monitor) => {
    try {
      const updated = await toggleMonitorEnabled(monitor.id, !monitor.enabled);
      setMonitors((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
      setGroupMembersMap((prev) => {
        const nextEntries = Object.entries(prev).map(([groupId, members]) => [
          groupId,
          members.map((member) => (member.id === updated.id ? updated : member)),
        ] as const);
        return Object.fromEntries(nextEntries);
      });
      showToast(updated.enabled ? 'Monitor resumed' : 'Monitor paused', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to update monitor', 'error');
    }
  };

  const handleSnooze = async (monitor: Monitor, durationMinutes: number) => {
    try {
      const window = await snoozeMonitor(monitor.id, { duration_minutes: durationMinutes });
      const updated: Monitor = { ...monitor, in_maintenance: true, maintenance_until: window.ends_at };
      setMonitors((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
      setGroupMembersMap((prev) => {
        const nextEntries = Object.entries(prev).map(([groupId, members]) => [
          groupId,
          members.map((member) => (member.id === updated.id ? updated : member)),
        ] as const);
        return Object.fromEntries(nextEntries);
      });
      showToast(`Alerts snoozed until ${formatDateTime(window.ends_at)}`, 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to snooze monitor', 'error');
    }
  };

  const toggleMonitorSelection = (id: string) => {
    setSelectedMonitorIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  };

  const clearSelection = () => {
    setSelectedMonitorIds(new Set());
    setSelectionMode(false);
    setGroupNameInput('');
    setTargetGroupId('');
  };

  // Collect all unique tags from monitors, plus any selected through the URL
  // that no monitor carries, so they can still be cleared.
  const allTags = useMemo(() => {
    const tagSet = new Set<string>(selectedTags);
    monitors.forEach((monitor) => {
      if (monitor.tags) {
        monitor.tags.forEach((tag) => tagSet.add(tag));
      }
    });
    return Array.from(tagSet).sort();
  }, [monitors, selectedTags]);

  // Types present in the workspace, with counts, most common first.
  // Derived from data so new monitor types appear here with no UI changes.
  const typeCounts = useMemo(() => {
    const counts = new Map<string, number>();
    monitors.forEach((monitor) => {
      counts.set(monitor.type, (counts.get(monitor.type) || 0) + 1);
    });
    return Array.from(counts.entries()).sort(
      (a, b) => b[1] - a[1] || a[0].localeCompare(b[0])
    );
  }, [monitors]);

  // Monitors whose alerts would notify nobody (server-computed per monitor in
  // shared/alertrouting). Drives the filter chip and, with it, the dashboard's
  // deep link.
  const unroutedCount = useMemo(
    () => monitors.filter((monitor) => monitor.enabled && monitor.alert_routing?.reachable === false).length,
    [monitors],
  );

  const toggleType = (type: string) => {
    const newSelected = new Set(selectedTypes);
    if (newSelected.has(type)) {
      newSelected.delete(type);
    } else {
      newSelected.add(type);
    }
    setSelectedTypes(newSelected);
  };

  // Toggle tag selection
  const toggleTag = (tag: string) => {
    const newSelected = new Set(selectedTags);
    if (newSelected.has(tag)) {
      newSelected.delete(tag);
    } else {
      newSelected.add(tag);
    }
    setSelectedTags(newSelected);
  };

  // Clear all tag filters
  const clearTagFilters = () => {
    setSelectedTags(new Set());
  };

  // Collect all monitor IDs that are members of a group
  const groupMemberIds = useMemo(() => {
    const memberIds = new Set<string>();
    Object.values(groupMembersMap).forEach((members) => {
      members.forEach((m) => memberIds.add(m.id));
    });
    return memberIds;
  }, [groupMembersMap]);

  const statusOf = (monitor: Monitor) => getEffectiveMonitorStatus(monitor, checkResultsMap[monitor.id] || []);

  // Search covers what the row shows: name, target (URL or host) and tags.
  const query = searchTerm.trim().toLowerCase();
  const monitorMatchesFilters = (monitor: Monitor) => {
    const matchesSearch =
      !query ||
      monitor.name.toLowerCase().includes(query) ||
      Boolean(monitorTarget(monitor)?.toLowerCase().includes(query)) ||
      Boolean(monitor.tags?.some((tag) => tag.toLowerCase().includes(query)));
    const matchesType = selectedTypes.size === 0 || selectedTypes.has(monitor.type);
    const status = statusOf(monitor);
    const matchesStatus = statusFilter === 'all' ||
      (statusFilter === 'up' && status === 'up') ||
      (statusFilter === 'down' && (status === 'down' || status === 'degraded')) ||
      (statusFilter === 'paused' && status === 'paused') ||
      (statusFilter === 'maintenance' && status === 'maintenance');
    const matchesTags = selectedTags.size === 0 ||
      Boolean(monitor.tags && monitor.tags.some((tag) => selectedTags.has(tag)));
    const matchesRouting =
      !unroutedOnly || (monitor.enabled && monitor.alert_routing?.reachable === false);
    return matchesSearch && matchesType && matchesStatus && matchesTags && matchesRouting;
  };

  // A group also matches when any of its (nested) members match, so searching
  // for a member surfaces the group it lives in.
  const groupHasMatchingMember = (groupId: string, visited: Set<string> = new Set()): boolean => {
    if (visited.has(groupId)) return false;
    visited.add(groupId);
    return (groupMembersMap[groupId] || []).some(
      (member) =>
        monitorMatchesFilters(member) ||
        (member.type === 'group' && groupHasMatchingMember(member.id, visited))
    );
  };

  const hasActiveFilters =
    searchTerm.trim() !== '' || selectedTypes.size > 0 || statusFilter !== 'all' || selectedTags.size > 0 ||
    unroutedOnly;

  const clearFilters = () => {
    setSearchTerm('');
    setSelectedTypes(new Set());
    setStatusFilter('all');
    setSelectedTags(new Set());
    setUnroutedOnly(false);
  };

  // Default order is severity then name, so what is broken comes first;
  // monitors arrive name-sorted, so the name order is a stable no-op.
  const orderMonitors = (items: Monitor[]) =>
    sortMode === 'status' && resultsReady ? sortBySeverity(items, statusOf, (m) => m.name) : sortMonitorsByName(items);

  // Filter monitors (exclude monitors that are members of a group - they only show under their group),
  // then show groups ahead of ungrouped monitors.
  const topLevel = monitors.filter((monitor) => {
    if (groupMemberIds.has(monitor.id)) return false;
    if (monitorMatchesFilters(monitor)) return true;
    return monitor.type === 'group' && groupHasMatchingMember(monitor.id);
  });
  const filteredMonitors = [
    ...orderMonitors(topLevel.filter((m) => m.type === 'group')),
    ...orderMonitors(topLevel.filter((m) => m.type !== 'group')),
  ];
  const hasGroups = filteredMonitors.some((m) => m.type === 'group');

  const selectedMonitor = monitors.find((m) => m.id === selectedMonitorId) || null;
  const selectedMonitors = monitors.filter((m) => selectedMonitorIds.has(m.id));
  const availableGroups = monitors
    .filter((m) => m.type === 'group' && !selectedMonitorIds.has(m.id))
    .sort((a, b) => a.name.localeCompare(b.name));
  const allFilteredSelected = filteredMonitors.length > 0 && filteredMonitors.every((m) => selectedMonitorIds.has(m.id));
  const canDeleteSelected = selectedMonitorIds.size > 0;
  const canCreateGroup = selectedMonitorIds.size > 0 && groupNameInput.trim().length > 0;
  const canMoveToGroup = targetGroupId.length > 0 && selectedMonitors.length > 0;

  useEffect(() => {
    if (!targetGroupId) return;
    if (targetGroupId === NO_GROUP_VALUE) return;
    if (!availableGroups.some((group) => group.id === targetGroupId)) {
      setTargetGroupId('');
    }
  }, [targetGroupId, availableGroups]);

  const toggleSelectAllFiltered = () => {
    if (allFilteredSelected) {
      setSelectedMonitorIds((prev) => {
        const next = new Set(prev);
        filteredMonitors.forEach((m) => next.delete(m.id));
        return next;
      });
      return;
    }
    setSelectedMonitorIds((prev) => {
      const next = new Set(prev);
      filteredMonitors.forEach((m) => next.add(m.id));
      return next;
    });
  };

  const handleBulkDelete = async () => {
    if (selectedMonitors.length === 0) return;
    setDeleting(true);
    try {
      await Promise.all(selectedMonitors.map((m) => deleteMonitor(m.id)));
      setMonitors(monitors.filter((m) => !selectedMonitorIds.has(m.id)));
      setSelectedMonitorIds(new Set());
      showToast('Selected monitors deleted', 'success');
      setConfirmBulkDelete(false);
    } catch (err: any) {
      showToast(err.message || 'Failed to delete selected monitors', 'error');
    } finally {
      setDeleting(false);
    }
  };

  const handleCreateGroup = async () => {
    const trimmedName = groupNameInput.trim();
    if (!trimmedName) {
      showToast('Group name is required', 'error');
      return;
    }
    if (selectedMonitors.length < 1) {
      showToast('Select at least one item to create a group', 'error');
      return;
    }

    try {
      await createMonitor({
        name: trimmedName,
        type: 'group',
        config: { monitor_ids: selectedMonitors.map((m) => m.id) },
        interval_seconds: 60,
        timeout_seconds: 30,
        enabled: true,
      });
      setGroupNameInput('');
      setSelectedMonitorIds(new Set());
      await loadMonitors();
      showToast('Group created', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to create group', 'error');
    }
  };

  const handleMoveToGroup = async () => {
    if (!targetGroupId) {
      showToast('Select a destination group first', 'error');
      return;
    }
    if (selectedMonitors.length === 0) {
      showToast('Select at least one monitor or group to move', 'error');
      return;
    }

    const isNoGroupDestination = targetGroupId === NO_GROUP_VALUE;
    const targetGroup = isNoGroupDestination
      ? null
      : monitors.find((m) => m.id === targetGroupId && m.type === 'group');
    if (!isNoGroupDestination && !targetGroup) {
      showToast('Destination group not found', 'error');
      return;
    }

    const selectedIds = selectedMonitors.map((m) => m.id);
    const selectedGroupIDs = selectedMonitors
      .filter((monitor) => monitor.type === 'group')
      .map((monitor) => monitor.id);

    if (!isNoGroupDestination) {
      if (selectedGroupIDs.includes(targetGroupId)) {
        showToast('Cannot move a group into itself', 'error');
        return;
      }

      const wouldCreateCycle = selectedGroupIDs.some((groupID) =>
        groupContainsGroup(groupID, targetGroupId, groupMembersMap)
      );
      if (wouldCreateCycle) {
        showToast('Cannot move a group into one of its descendants', 'error');
        return;
      }
    }

    const removalsByGroup: Record<string, string[]> = {};

    for (const [groupId, members] of Object.entries(groupMembersMap)) {
      if (!isNoGroupDestination && groupId === targetGroupId) continue;
      const overlappingIds = members
        .filter((member) => selectedIds.includes(member.id))
        .map((member) => member.id);
      if (overlappingIds.length > 0) {
        removalsByGroup[groupId] = overlappingIds;
      }
    }

    const hasRemovals = Object.keys(removalsByGroup).length > 0;

    if (isNoGroupDestination && !hasRemovals) {
      showToast('Selected items are not in any group', 'error');
      return;
    }

    const targetMemberIds = new Set(
      isNoGroupDestination ? [] : (groupMembersMap[targetGroupId] || []).map((m) => m.id)
    );
    const idsToAdd = isNoGroupDestination
      ? []
      : selectedIds.filter((id) => !targetMemberIds.has(id));

    if (!isNoGroupDestination && !hasRemovals && idsToAdd.length === 0) {
      showToast('Selected items are already in the target group', 'error');
      return;
    }

    try {
      if (!isNoGroupDestination && idsToAdd.length > 0) {
        await addMonitorsToGroup(targetGroupId, idsToAdd);
      }

      if (hasRemovals) {
        await Promise.all(
          Object.entries(removalsByGroup).map(([groupId, monitorIds]) =>
            removeMonitorsFromGroup(groupId, monitorIds)
          )
        );
      }

      setSelectedMonitorIds(new Set());
      await loadMonitors();
      showToast(isNoGroupDestination
          ? `Removed ${selectedMonitors.length} item(s) from all groups`
          : `Moved ${selectedMonitors.length} item(s) to ${targetGroup?.name}`, 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to move monitors', 'error');
    }
  };

  // Status counts for the filter chips. These count every monitor, group
  // members included, under the same rule the status filter applies.
  const statusCounts: Record<StatusFilter, number> = { all: monitors.length, up: 0, down: 0, paused: 0, maintenance: 0 };
  monitors.forEach((m) => {
    const status = statusOf(m);
    if (status === 'up') statusCounts.up += 1;
    else if (status === 'down' || status === 'degraded') statusCounts.down += 1;
    else if (status === 'paused') statusCounts.paused += 1;
    else if (status === 'maintenance') statusCounts.maintenance += 1;
  });
  // Up and Down always show; the chosen states only when something is in them.
  const visibleStatusFilters = MONITOR_STATUS_FILTERS.filter(
    (id) => id === 'all' || id === 'up' || id === 'down' || statusCounts[id] > 0 || statusFilter === id
  );

  const isEmptyWorkspace = !loading && !error && monitors.length === 0;
  const rowLayout = ROW_LAYOUTS[selectedMonitor ? 'panel' : 'list'];
  const listStyle = leadStyle(selectionMode, hasGroups);
  // On phones the chip rows scroll sideways instead of wrapping into a wall.
  const scrollRow =
    '-mx-4 flex flex-nowrap items-center gap-1.5 overflow-x-auto px-4 [scrollbar-width:none] sm:-mx-6 sm:px-6 lg:mx-0 lg:flex-wrap lg:overflow-visible lg:px-0';

  return (
    <div className="space-y-6">
      <Suspense fallback={null}>
        <MonitorListUrlSync onParams={applyUrlParams} />
      </Suspense>
      {/* Header */}
      <PageHeader
        title="Monitors"
        action={
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              className="hidden sm:inline-flex"
              icon={<Download strokeWidth={1.75} />}
              onClick={handleExport}
              disabled={exporting}
              loading={exporting}
            >
              {exporting ? 'Exporting…' : 'Export'}
            </Button>
            {canWrite && (
              <Button variant="ghost" size="sm" className="hidden sm:inline-flex" icon={<Upload strokeWidth={1.75} />} asChild>
                <Link href="/monitors/import">Import</Link>
              </Button>
            )}
            {canWrite && !isEmptyWorkspace && (
              <Button variant="accent" size="sm" icon={<Plus strokeWidth={1.75} />} asChild>
                <Link href="/monitors/new">Add monitor</Link>
              </Button>
            )}
            {/* Phones: one primary button; export and import behind ⋮. */}
            <div className="sm:hidden">
              <RowMenu label="More actions">
                <RowMenuItem onSelect={handleExport} icon={<Download strokeWidth={1.75} />}>
                  {exporting ? 'Exporting…' : 'Export'}
                </RowMenuItem>
                {canWrite && (
                  <RowMenuItem href="/monitors/import" icon={<Upload strokeWidth={1.75} />}>Import</RowMenuItem>
                )}
              </RowMenu>
            </div>
          </div>
        }
      />

      {/* Filters */}
      {!isEmptyWorkspace && (
      <div className="space-y-3">
        <div className="flex flex-col gap-2.5 lg:flex-row lg:flex-wrap lg:items-center lg:gap-3">
          <div className="relative w-full lg:w-64">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
            <input
              type="search"
              placeholder="Search name, URL, host or tag"
              aria-label="Search monitors by name, URL, host or tag"
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              className="input input-sm h-8 py-0 !pl-8"
            />
          </div>

          <div role="group" aria-label="Status" className={scrollRow}>
            {visibleStatusFilters.map((id) => (
              <FilterChip
                key={id}
                selected={statusFilter === id}
                count={statusCounts[id]}
                onClick={() => setStatusFilter(id)}
              >
                {STATUS_FILTER_LABEL[id]}
              </FilterChip>
            ))}
          </div>

          {/* Wraps onto its own line as a unit when the row is too narrow. */}
          <div className={`${scrollRow} lg:min-w-max lg:flex-1 lg:flex-nowrap`}>
            {/* Type Filter Toggle */}
            <FilterChip
              selected={selectedTypes.size > 0}
              count={selectedTypes.size > 0 ? selectedTypes.size : undefined}
              icon={<Layers strokeWidth={1.75} />}
              onClick={() => setShowTypeFilter(!showTypeFilter)}
              aria-expanded={showTypeFilter}
            >
              <span className="inline-flex items-center gap-1">
                Type
                <ChevronDown
                  className={`h-3 w-3 transition-transform ${showTypeFilter ? 'rotate-180' : ''}`}
                  strokeWidth={1.75}
                />
              </span>
            </FilterChip>

            {/* Tag Filter Toggle */}
            {allTags.length > 0 && (
              <FilterChip
                selected={selectedTags.size > 0}
                count={selectedTags.size > 0 ? selectedTags.size : undefined}
                icon={<Tag strokeWidth={1.75} />}
                onClick={() => setShowTagFilter(!showTagFilter)}
                aria-expanded={showTagFilter}
              >
                <span className="inline-flex items-center gap-1">
                  Tags
                  <ChevronDown
                    className={`h-3 w-3 transition-transform ${showTagFilter ? 'rotate-180' : ''}`}
                    strokeWidth={1.75}
                  />
                </span>
              </FilterChip>
            )}

            {(unroutedCount > 0 || unroutedOnly) && (
              <FilterChip
                selected={unroutedOnly}
                count={unroutedCount > 0 ? unroutedCount : undefined}
                icon={<BellOff strokeWidth={1.75} />}
                onClick={() => setUnroutedOnly((v) => !v)}
                title="Monitors whose alerts would notify nobody"
              >
                No alert route
              </FilterChip>
            )}

            {filteredMonitors.length > 0 && (
              <RowLayoutContext.Provider value={rowLayout}>
                <NarrowSortChip sort={sortMode} onSortChange={setSortMode} />
              </RowLayoutContext.Provider>
            )}

            {!selectionMode && filteredMonitors.length > 0 && (
              <FilterChip
                className="lg:ml-auto"
                icon={<CheckSquare strokeWidth={1.75} />}
                onClick={() => setSelectionMode(true)}
              >
                Select
              </FilterChip>
            )}
          </div>
        </div>

        {/* Type Filter Dropdown */}
        {showTypeFilter && typeCounts.length > 0 && (
          <div className="glass p-4">
            <div className="mb-3 flex h-7 items-center justify-between">
              <span className="text-ui font-medium text-slate-300">Filter by type</span>
              {selectedTypes.size > 0 && (
                <Button variant="subtle" size="xs" onClick={() => setSelectedTypes(new Set())}>
                  Clear types
                </Button>
              )}
            </div>
            <div className="flex flex-wrap gap-1.5">
              {typeCounts.map(([type, count]) => (
                <FilterChip
                  key={type}
                  selected={selectedTypes.has(type)}
                  count={count}
                  onClick={() => toggleType(type)}
                >
                  {monitorTypeLabel(type)}
                </FilterChip>
              ))}
            </div>
          </div>
        )}

        {/* Tag Filter Dropdown */}
        {showTagFilter && allTags.length > 0 && (
          <div className="glass p-4">
            <div className="mb-3 flex h-7 items-center justify-between">
              <span className="text-ui font-medium text-slate-300">Filter by tags</span>
              {selectedTags.size > 0 && (
                <Button variant="subtle" size="xs" onClick={clearTagFilters}>
                  Clear tags
                </Button>
              )}
            </div>
            <div className="flex flex-wrap gap-1.5">
              {allTags.map((tag) => (
                <TagPill
                  key={tag}
                  tag={tag}
                  size="sm"
                  selected={selectedTags.has(tag)}
                  onClick={() => toggleTag(tag)}
                />
              ))}
            </div>
          </div>
        )}
      </div>
      )}

      {/* Error */}
      {error && (
        <Callout
          tone="danger"
          action={<Button variant="danger" size="xs" onClick={loadMonitors}>Retry</Button>}
        >
          {error}
        </Callout>
      )}

      {/* Content */}
      {/* Skeleton on the first load only; a refetch keeps the rows on screen. */}
      {loading && monitors.length === 0 ? (
        <TableSkeleton rows={6} columns={4} label="Loading monitors" />
      ) : monitors.length === 0 ? (
        !error && (
          <SharedEmptyState
            icon={<Activity strokeWidth={1.5} />}
            title="No monitors yet"
            description={
              canWrite
                ? 'Add a monitor to start checking an endpoint, host or service.'
                : 'Monitors appear here once someone with write access adds one.'
            }
            action={canWrite ? { label: 'Add monitor', href: '/monitors/new', icon: <Plus strokeWidth={1.75} /> } : undefined}
          />
        )
      ) : (
        <RowLayoutContext.Provider value={rowLayout}>
        <div className={`grid gap-6 ${selectedMonitor ? 'lg:grid-cols-[minmax(0,1fr)_320px]' : ''}`}>
          {/* Monitor List */}
          <div className="glass min-w-0 self-start overflow-hidden" style={listStyle}>
            {/* Bulk actions bar (selection mode is entered from the filter row) */}
            {selectionMode && filteredMonitors.length > 0 && (
              <div className="flex flex-wrap items-center gap-2 border-b border-divider px-4 py-3">
                {/* Selection mode toggle */}
                <Button
                  variant="ghost"
                  size="xs"
                  icon={<X strokeWidth={1.75} />}
                  onClick={() => {
                    if (selectionMode) {
                      clearSelection();
                    } else {
                      setSelectionMode(true);
                    }
                  }}
                >
                  {selectionMode ? 'Exit select' : 'Select'}
                </Button>

                {/* Selection actions - only show when in selection mode */}
                {selectionMode && (
                  <>
                    <Button variant="ghost" size="xs" onClick={toggleSelectAllFiltered}>
                      {allFilteredSelected ? 'Deselect all' : 'Select all'}
                    </Button>

                    {selectedMonitorIds.size > 0 && (
                      <>
                        <span className="px-1 text-xs font-medium tabular-nums text-slate-300">
                          {selectedMonitorIds.size} selected
                        </span>

                        <div className="h-4 w-px bg-divider" />

                        {/* Group creation */}
                        <div className="flex items-center gap-1.5">
                          <input
                            type="text"
                            placeholder="New group name…"
                            aria-label="New group name"
                            value={groupNameInput}
                            onChange={(e) => setGroupNameInput(e.target.value)}
                            className="input input-xs h-7 !w-32"
                          />
                          <Button
                            variant="ghost"
                            size="xs"
                            icon={<FolderPlus strokeWidth={1.75} />}
                            onClick={handleCreateGroup}
                            disabled={!canCreateGroup}
                          >
                            Group
                          </Button>
                        </div>

                        {/* Move to existing group */}
                        <div className="flex items-center gap-1.5">
                          <select
                            value={targetGroupId}
                            onChange={(e) => setTargetGroupId(e.target.value)}
                            aria-label="Move selected to group"
                            className="input input-xs h-7 !w-36"
                          >
                            <option value="">Move to group…</option>
                            <option value={NO_GROUP_VALUE}>No group</option>
                            {availableGroups.map((group) => (
                              <option key={group.id} value={group.id}>
                                {group.name}
                              </option>
                            ))}
                          </select>
                          <Button
                            variant="ghost"
                            size="xs"
                            icon={<Move strokeWidth={1.75} />}
                            onClick={handleMoveToGroup}
                            disabled={!canMoveToGroup}
                          >
                            Move
                          </Button>
                        </div>

                        <div className="h-4 w-px bg-divider" />

                        <Button
                          variant="ghost"
                          size="xs"
                          icon={<Bell strokeWidth={1.75} />}
                          onClick={() => setShowBulkAlerting(true)}
                        >
                          Edit alerting…
                        </Button>

                        <Button
                          variant="danger"
                          size="xs"
                          icon={<Trash2 strokeWidth={1.75} />}
                          onClick={() => setConfirmBulkDelete(true)}
                        >
                          Delete
                        </Button>
                      </>
                    )}
                  </>
                )}
              </div>
            )}
            
            {filteredMonitors.length > 0 && <ListHeader sort={sortMode} onSortChange={setSortMode} />}

            {filteredMonitors.length === 0 ? (
              <SharedEmptyState
                variant="inline"
                icon={<Search strokeWidth={1.5} />}
                title="No monitors match these filters"
                description={query ? `Nothing matches “${searchTerm.trim()}” with the current filters.` : undefined}
                action={
                  hasActiveFilters ? (
                    <Button variant="ghost" size="sm" icon={<X strokeWidth={1.75} />} onClick={clearFilters}>
                      Clear filters
                    </Button>
                  ) : undefined
                }
              />
            ) : (
              <div className="divide-y divide-divider">
                {filteredMonitors.map((monitor) => (
                  monitor.type === 'group' ? (
                    <GroupCard
                      key={monitor.id}
                      monitor={monitor}
                      results={checkResultsMap[monitor.id] || []}
                      members={groupMembersMap[monitor.id] || []}
                      groupMembersMap={groupMembersMap}
                      memberResults={checkResultsMap}
                      isSelected={monitor.id === selectedMonitorId}
                      isChecked={selectedMonitorIds.has(monitor.id)}
                      onToggleSelect={() => toggleMonitorSelection(monitor.id)}
                      onToggleMonitorSelection={toggleMonitorSelection}
                      onDeleteMonitor={requestDeleteById}
                      onToggleEnabled={handleToggleEnabled}
                      onSnooze={handleSnooze}
                      selectionMode={selectionMode}
                      onClick={() => selectMonitor(monitor.id)}
                      onDelete={() => requestDeleteById(monitor.id)}
                      onSelectMonitor={selectMonitor}
                      selectedMonitorId={selectedMonitorId}
                      selectedMonitorIds={selectedMonitorIds}
                      orderMembers={orderMonitors}
                      autoExpand={hasActiveFilters && groupHasMatchingMember(monitor.id)}
                      memberMatcher={hasActiveFilters ? monitorMatchesFilters : undefined}
                      groupHasMatch={(groupId) => groupHasMatchingMember(groupId)}
                    />
                  ) : (
                    <MonitorRow
                      key={monitor.id}
                      monitor={monitor}
                      results={checkResultsMap[monitor.id] || []}
                      isSelected={monitor.id === selectedMonitorId}
                      isChecked={selectedMonitorIds.has(monitor.id)}
                      onToggleSelect={() => toggleMonitorSelection(monitor.id)}
                      selectionMode={selectionMode}
                      onClick={() => selectMonitor(monitor.id)}
                      onDelete={() => requestDeleteById(monitor.id)}
                      onToggleEnabled={handleToggleEnabled}
                      onSnooze={handleSnooze}
                    />
                  )
                ))}
              </div>
            )}
          </div>

          {/* Detail Panel */}
          {selectedMonitor && <div className="hidden lg:block">
            <div className="dashboard-scroll sticky top-6 max-h-[calc(100vh-3rem)] overflow-y-auto overscroll-contain rounded-2xl ![scrollbar-gutter:auto]">
              <DetailPanel
                monitor={selectedMonitor}
                results={selectedMonitor ? checkResultsMap[selectedMonitor.id] || [] : []}
                members={selectedMonitor ? groupMembersMap[selectedMonitor.id] || [] : []}
                memberResults={checkResultsMap}
                onSelectMember={setSelectedMonitorId}
                onClose={() => setSelectedMonitorId(null)}
              />
            </div>
          </div>}
        </div>
        </RowLayoutContext.Provider>
      )}

      {showBulkAlerting && (
        <BulkAlertingModal
          monitorIds={Array.from(selectedMonitorIds)}
          onDone={() => {
            setShowBulkAlerting(false);
            setSelectedMonitorIds(new Set());
            setSelectionMode(false);
            showToast('Alerting settings updated', 'success');
            loadMonitors();
          }}
          onCancel={() => setShowBulkAlerting(false)}
        />
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title={pendingDelete?.type === 'group' ? 'Delete group' : 'Delete monitor'}
        description={
          pendingDelete
            ? `“${pendingDelete.name}” will be removed along with its check history. This cannot be undone.`
            : undefined
        }
        confirmLabel="Delete"
        loading={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setPendingDelete(null)}
      />

      <ConfirmDialog
        open={confirmBulkDelete}
        title={`Delete ${selectedMonitors.length} selected monitor${selectedMonitors.length === 1 ? '' : 's'}`}
        description="The selected monitors and their check history will be removed. This cannot be undone."
        confirmLabel="Delete selected"
        loading={deleting}
        onConfirm={handleBulkDelete}
        onCancel={() => !deleting && setConfirmBulkDelete(false)}
      />
    </div>
  );
}
