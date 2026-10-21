'use client';

import PrometheusDetails from '@/components/monitors/PrometheusDetails';
import { createPortal } from 'react-dom';
import { createContext, Suspense, useCallback, useContext, useId, useState, useEffect, useMemo, useRef, type CSSProperties, type KeyboardEvent, type MouseEvent } from 'react';
import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { Plus, Download, Upload, Search, Tag, ChevronRight, Activity, FolderPlus, Move, Trash2, Bell, BellOff, Layers, X, Pencil, Copy, Pause, Play } from 'lucide-react';
import RowMenu, { RowMenuItem, RowMenuSeparator, useCloseRowMenu } from '@/components/ui/RowMenu';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import IconButton from '@/components/ui/IconButton';
import SharedSelectCheckbox from '@/components/ui/SelectCheckbox';
import Checkbox from '@/components/ui/Checkbox';
import Page from '@/components/ui/Page';
import SortableHeader from '@/components/ui/SortableHeader';
import LiveStatus from '@/components/ui/LiveStatus';
import { RelativeTime } from '@/components/ui/RelativeTime';
import { FOCUS_RING, FOCUS_RING_INSET } from '@/components/ui/focus';
import { useOptionalAlertEvents } from '@/components/alerts/AlertStreamProvider';
import FilterChip from '@/components/ui/FilterChip';
import FilterPopover, { type FilterOption } from '@/components/ui/FilterPopover';
import PageHeader from '@/components/ui/PageHeader';
import SharedEmptyState from '@/components/ui/EmptyState';
import TableSkeleton from '@/components/ui/TableSkeleton';
import TypeBadge from '@/components/ui/TypeBadge';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import CopyableTarget from '@/components/ui/CopyableTarget';
import { useToast } from '@/components/ui/ToastProvider';
import { BulkAlertingModal } from '@/components/monitors/BulkAlertingModal';
import CheckTrace from '@/components/monitors/CheckTrace';
import TagPill, { getTagColor } from '@/components/monitors/TagPill';
import { type LiveLoadOptions, useLiveRefresh } from '@/lib/useLiveRefresh';
import { flashStyle, useRowFlash } from '@/lib/useRowFlash';
import { Monitor, CheckResult, AgentMetrics, HTTPDisplayValue } from '@/lib/types';
import {
  deleteMonitor,
  MONITOR_RESULTS_BATCH_MAX_IDS,
  createMonitor,
  addMonitorsToGroup,
  removeMonitorsFromGroup,
  getSyntheticBrowserScreenshotUrl,
  toggleMonitorEnabled,
  exportMonitors,
  snoozeMonitor,
} from '@/lib/api';
import {
  DEFAULT_MONITOR_SORT,
  formatMonitorListParams,
  getAllMonitors,
  MONITOR_SORT_DEFAULT_DIR,
  MONITOR_SORT_KEYS,
  MONITOR_STATUS_FILTERS,
  MonitorListParams,
  MonitorListSort,
  MonitorSortContext,
  MonitorSortKey,
  MonitorStatusFilter,
  parseMonitorListParams,
  sortMonitorList,
  sortMonitorsByName,
} from '@/lib/monitor-list';
import { fetchResultsBatches, resultsBatchError } from '@/lib/monitor-results';
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
} from '@/lib/monitor-utils';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';
import { formatDateTime, formatLatency, formatUptime } from '@/lib/format';
import {
  CHECK_RESULT_BAR,
  MONITOR_STATUS_DOT,
  MONITOR_STATUS_LABEL,
  MONITOR_STATUS_TONE,
  TONE_TEXT,
} from '@/lib/status-tones';

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

// Live refresh: the monitor list is one request and polls every 30s. Results
// come from GET /monitors/results/batch, MONITOR_RESULTS_BATCH_MAX_IDS
// monitors per request with every chunk in flight at once, so the first load
// ranks the list in one round trip. A background tick refetches only the
// monitors that changed state, that an alert event named, or whose next
// check is due, at most this many per tick (stalest first): one request.
const RESULTS_PER_TICK = MONITOR_RESULTS_BATCH_MAX_IDS;

// ── Row layout ───────────────────────────────────────────────────────────
//
// Header, monitor rows and group rows share one grid template, so every
// column lines up without spacer elements. The lead column holds the
// selection checkbox, the group chevron slot and the status dot; its width
// is the --row-lead variable the list sets. Below the wide breakpoint a row
// is name + compact trace + menu (--row-lead-narrow). The chevron keeps its
// slot at every width: it is the only way to reach members for Select mode,
// and a group expanded at lg must stay collapsible when the preview opens
// and the wide columns drop out. While the preview panel takes its width,
// the wide columns start at xl with trace and success only (the panel has
// the latency and the recent checks) and the full set returns at 2xl.
//
// The trace column is the trace's own fixed width (20 × 5px bars + gaps =
// 79px, CheckTrace): extra width never stretches it. On very wide screens
// (3xl list, 4xl with the preview) the target moves out of the name cell
// into its own column, so the room goes to the name and the target.
interface RowLayout {
  cols: string;
  header: string;
  /** Trace and success columns. */
  wide: string;
  /** Latency and last-check columns. */
  full: string;
  narrow: string;
  /** The target column (very wide screens only). */
  target: string;
  /** The target under the name, where there is no target column. */
  targetInline: string;
}

const ROW_LAYOUTS: Record<'list' | 'panel', RowLayout> = {
  list: {
    cols:
      'grid grid-cols-[var(--row-lead-narrow)_minmax(0,1fr)_auto_32px] ' +
      'lg:grid-cols-[var(--row-lead)_minmax(0,1fr)_80px_64px_72px_88px_32px] ' +
      '3xl:grid-cols-[var(--row-lead)_minmax(0,3fr)_minmax(0,2fr)_80px_72px_80px_96px_32px]',
    header: 'hidden lg:grid',
    wide: 'hidden lg:flex',
    full: 'hidden lg:flex',
    narrow: 'flex lg:hidden',
    target: 'hidden 3xl:flex',
    targetInline: '3xl:hidden',
  },
  panel: {
    cols:
      'grid grid-cols-[var(--row-lead-narrow)_minmax(0,1fr)_auto_32px] ' +
      'xl:grid-cols-[var(--row-lead)_minmax(0,1fr)_80px_64px_32px] ' +
      '2xl:grid-cols-[var(--row-lead)_minmax(0,1fr)_80px_64px_72px_88px_32px] ' +
      '4xl:grid-cols-[var(--row-lead)_minmax(0,3fr)_minmax(0,2fr)_80px_72px_80px_96px_32px]',
    header: 'hidden xl:grid',
    wide: 'hidden xl:flex',
    full: 'hidden 2xl:flex',
    narrow: 'flex xl:hidden',
    target: 'hidden 4xl:flex',
    targetInline: '4xl:hidden',
  },
};

const RowLayoutContext = createContext<RowLayout>(ROW_LAYOUTS.list);

function useRowLayout() {
  return useContext(RowLayoutContext);
}

/**
 * Lead column width: [checkbox] [chevron slot] dot, 8px apart. The checkbox
 * slot is reserved whenever rows are selectable (it reveals on hover), so
 * starting a selection never shifts the row.
 */
function leadWidth(selectable: boolean, hasGroups: boolean): number {
  return 8 + (selectable ? 16 + 8 : 0) + (hasGroups ? 24 + 8 : 0);
}

function leadStyle(selectable: boolean, hasGroups: boolean): CSSProperties {
  return {
    '--row-lead': `${leadWidth(selectable, hasGroups)}px`,
    '--row-lead-narrow': `${leadWidth(selectable, hasGroups)}px`,
  } as CSSProperties;
}

// Rows are selectable only for writers: every bulk action is a write.
const SelectableContext = createContext(false);

// Fills step up: hover (well) < previewed (well-hover) < checked
// (well-strong), so hovering a row never looks like checking it. Selection is
// never orange: orange is for actions. Keyboard focus lands on the row's name
// link and rings the whole row in the focus colour.
function rowStateClass(isSelected: boolean, isChecked: boolean) {
  const focus = 'has-[.row-link:focus-visible]:shadow-[inset_0_0_0_2px_var(--focus)]';
  if (isChecked) return `bg-well-strong ${focus}`;
  if (isSelected) return `bg-well-hover ${focus}`;
  return `hover:bg-well active:bg-well-hover ${focus}`;
}

// Rows whose status changed since the last refresh flash once (useRowFlash);
// the list provides the class per monitor id.
const RowFlashContext = createContext<(id: string) => string>(() => '');

// Arrow keys move between the rows' name links (each row is one link in
// the tab order, plus its copy and ⋮ controls).
function handleRowArrowKeys(event: KeyboardEvent<HTMLElement>) {
  if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
  const current = event.target as HTMLElement;
  if (!current.classList.contains('row-link')) return;
  const links = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('.row-link'));
  const next = links[links.indexOf(current) + (event.key === 'ArrowDown' ? 1 : -1)];
  if (!next) return;
  event.preventDefault();
  next.focus();
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
  return (
    <span
      role="img"
      aria-label={label}
      title={label}
      className={`h-2 w-2 flex-shrink-0 rounded-full transition-colors duration-slow ${fill}`}
    />
  );
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
        {/* Out of the tab order: copy is also on the preview and detail
            pages, and a row should cost one Tab stop, not three. */}
        <CopyableTarget
          value={target}
          label={monitorTargetLabel(monitor.type)}
          size="sm"
          revealOnHover
          focusable={false}
          textClassName="text-slate-500"
        />
      </span>
    </>
  );
}

/** The target in its own column (very wide layouts). Groups have none. */
function TargetCell({ monitor }: { monitor?: Monitor }) {
  const layout = useRowLayout();
  return (
    <div role="cell" className={`${layout.target} min-w-0 items-center`}>
      {monitor && <TargetLine monitor={monitor} />}
    </div>
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
 * A paused monitor's figures are history, so they are muted rather than
 * shown as live health (a green trace weeks old reads as "up").
 */
function RowMetrics({
  results,
  showLatency = true,
  paused = false,
}: {
  results: CheckResult[];
  showLatency?: boolean;
  paused?: boolean;
}) {
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
      <div role="cell" className={`${layout.narrow} flex-col items-end gap-1`}>
        <CheckTrace results={results} slots={10} size="sm" muted={paused} />
        <span className={`text-2xs tabular-nums ${paused ? 'text-slate-500' : 'text-slate-400'}`} title={successTitle}>{success}</span>
      </div>
      <div role="cell" className={`${layout.wide} min-w-0 justify-end`}>
        <CheckTrace results={results} slots={RECENT_CHECKS} muted={paused} className="flex-none" />
      </div>
      <div
        role="cell"
        className={`${layout.wide} justify-end text-xs tabular-nums ${paused ? 'text-slate-500' : 'text-slate-300'}`}
        title={successTitle}
      >
        {success}
      </div>
      <div role="cell" className={`${layout.full} justify-end text-xs tabular-nums text-slate-400`}>
        {showLatency && latency.samples > 0 ? formatLatency(latency.median) : '—'}
      </div>
      <div role="cell" className={`${layout.full} justify-end whitespace-nowrap text-xs tabular-nums text-slate-500`}>
        <RelativeTime value={latest?.created_at} fallback="Never" />
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
          {/* slate-400, not the caption step: the chip's well-hover sits on a
              hovered or checked row, a stacked fill slate-500 can't clear. */}
          {displayValueLabel(v) && <span className="shrink-0 text-slate-400">{displayValueLabel(v)}</span>}
          <span className="truncate">{v.value}</span>
        </span>
      ))}
    </span>
  );
}

// The row's selection box is always in the DOM (its slot is reserved, so
// nothing shifts) but only shows on row hover or keyboard focus — or always
// once a selection exists (`pinned`), and always on touch screens, which
// have no hover to reveal it.
const REVEAL_ON_HOVER =
  'opacity-0 transition-opacity duration-fast ease-standard focus-within:opacity-100 group-hover:opacity-100 [@media(hover:none)]:opacity-100';

function SelectCheckbox({
  checked,
  onChange,
  pinned,
  label,
}: {
  checked: boolean;
  onChange: () => void;
  pinned: boolean;
  label?: string;
}) {
  const selectable = useContext(SelectableContext);
  if (!selectable) return null;
  return (
    <SharedSelectCheckbox
      checked={checked}
      onChange={onChange}
      label={label}
      // mr-auto: the lead cell is right-aligned (dot last), so the box keeps
      // the column's left edge whether or not a chevron sits between.
      className={`mr-auto ${pinned || checked ? 'opacity-100' : REVEAL_ON_HOVER}`}
    />
  );
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
    <div
      role="cell"
      className="flex justify-end opacity-100 transition-opacity focus-within:opacity-100 md:opacity-0 md:focus-within:opacity-100 md:group-hover:opacity-100 md:group-has-[.row-link:focus-visible]:opacity-100"
    >
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
  const flashClass = useContext(RowFlashContext);
  const status = getEffectiveMonitorStatus(monitor, results);
  const latestResult = results[0];
  const agentMetrics = isAgentMetrics(latestResult?.metrics_data) ? latestResult.metrics_data : null;
  const displayValues = getHTTPDisplayValues(results);

  return (
    <div
      role="row"
      onClick={onClick}
      style={flashStyle(MONITOR_STATUS_TONE[status])}
      className={`group ${layout.cols} cursor-pointer items-center gap-x-3 px-4 py-1.5 transition-colors ${rowStateClass(isSelected, isChecked)} ${flashClass(monitor.id)}`}
    >
      {/* Lead: [checkbox] [chevron slot] dot */}
      <div role="cell" className="flex items-center justify-end gap-2">
        <SelectCheckbox
          checked={isChecked}
          onChange={onToggleSelect}
          pinned={selectionMode}
          label={`Select ${monitor.name}`}
        />
        <StatusDot status={status} />
      </div>

      <div role="cell" className="min-w-0">
        <div className="flex h-5 min-w-0 items-center gap-2 overflow-hidden">
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
        <div className="mt-1 flex h-4 min-w-0 items-center gap-2">
          <TypeBadge type={monitor.type} config={monitor.config} size="sm" />
          <span className={`flex min-w-0 items-center ${layout.targetInline}`}>
            <TargetLine monitor={monitor} />
          </span>
          {monitor.type === 'agent' && agentMetrics && <AgentMetricsMini metrics={agentMetrics} />}
        </div>
      </div>

      <TargetCell monitor={monitor} />

      <RowMetrics results={results} paused={status === 'paused'} />

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
  const flashClass = useContext(RowFlashContext);
  const membersId = useId();
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
  // The summary takes the group's own rolled-up status tone, so it always
  // agrees with the group's dot (a group can roll a down member up to
  // degraded); no members or no status yet stays neutral.
  const healthTone =
    members.length > 0 && (status === 'down' || status === 'degraded' || status === 'up')
      ? TONE_TEXT[MONITOR_STATUS_TONE[status]]
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
    <div role="none">
      {/* Group Header */}
      <div
        role="row"
        onClick={onClick}
        style={flashStyle(MONITOR_STATUS_TONE[status])}
        className={`group ${layout.cols} cursor-pointer items-center gap-x-3 px-4 py-1.5 transition-colors ${rowStateClass(isSelected, isChecked)} ${flashClass(monitor.id)}`}
      >
        <div role="cell" className="flex items-center justify-end gap-2">
          <SelectCheckbox
            checked={isChecked}
            onChange={onToggleSelect}
            pinned={selectionMode}
            label={`Select ${monitor.name}`}
          />
          <button
            type="button"
            onClick={(e) => { e.stopPropagation(); setExpanded(!expanded); }}
            aria-expanded={expanded}
            aria-controls={expanded ? membersId : undefined}
            aria-label={`${expanded ? 'Collapse' : 'Expand'} ${monitor.name}`}
            className={`flex h-6 w-6 flex-shrink-0 items-center justify-center rounded-md text-slate-400 transition-colors hover:bg-well-hover hover:text-white active:bg-control-hover ${FOCUS_RING}`}
          >
            <ChevronRight
              className={`h-4 w-4 transition-transform duration-base ease-standard ${expanded ? 'rotate-90' : ''}`}
              strokeWidth={1.75}
            />
          </button>
          <StatusDot status={status} />
        </div>

        <div role="cell" className="min-w-0">
          <div className="flex h-5 min-w-0 items-center gap-2 overflow-hidden">
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
          <div className="mt-1 flex h-4 min-w-0 items-center gap-2">
            <TypeBadge type="group" size="sm" />
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

        <TargetCell />

        {/* A group's own checks are its roll-up; latency means nothing there. */}
        <RowMetrics results={results} showLatency={false} paused={status === 'paused'} />

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
        <div
          id={membersId}
          role="none"
          className="dashboard-scroll max-h-[60vh] animate-fade-in overflow-y-auto overscroll-contain border-t border-divider bg-well pl-6 ![scrollbar-gutter:auto]"
        >
          {hiddenMemberCount > 0 && (
            <div role="row">
              <p role="cell" className="px-4 pb-1 pt-2.5 text-xs text-slate-500">
                Showing {visibleMembers.length} matching of {members.length} members
              </p>
            </div>
          )}
          <div role="none" className="divide-y divide-divider">
          {visibleMembers.map((member) => {
            const memberKey = `${monitor.id}:${member.id}`;
            if (member.type === 'group') {
              const cycleDetected = nextVisitedGroupIDs.has(member.id);
              return (
                <div key={memberKey} role="none">
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
                      <div role="row">
                        <p role="cell" className="px-4 pb-2.5 text-xs text-rose-300">
                          Cycle detected in group nesting. Expansion stopped.
                        </p>
                      </div>
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

// Sort choices for the narrow layouts, where the column header is hidden
// (phones, or the preview panel taking the width): one select covering
// every column and direction.
const SORT_LABEL: Record<MonitorSortKey, string> = {
  status: 'Status',
  name: 'Name',
  success: 'Success',
  latency: 'Latency',
  last_check: 'Last check',
};

const SORT_DIR_LABEL: Record<MonitorSortKey, { asc: string; desc: string }> = {
  status: { asc: 'worst first', desc: 'best first' },
  name: { asc: 'A to Z', desc: 'Z to A' },
  success: { asc: 'lowest first', desc: 'highest first' },
  latency: { asc: 'fastest first', desc: 'slowest first' },
  last_check: { asc: 'stalest first', desc: 'most recent first' },
};

function sortDescription(sort: SortMode): string {
  return `${SORT_LABEL[sort.key]}, ${SORT_DIR_LABEL[sort.key][sort.dir]}`;
}

function NarrowSortSelect({ sort, onSortChange }: { sort: SortMode; onSortChange: (sort: SortMode) => void }) {
  const layout = useRowLayout();
  return (
    <label className={`${layout.narrow} flex-shrink-0 items-center`}>
      <span className="sr-only">Sort monitors by</span>
      <select
        value={`${sort.key}:${sort.dir}`}
        onChange={(e) => {
          const [key, dir] = e.target.value.split(':') as [MonitorSortKey, 'asc' | 'desc'];
          onSortChange({ key, dir });
        }}
        className="input input-sm h-8 !w-auto py-0"
      >
        {MONITOR_SORT_KEYS.flatMap((key) => {
          const first = MONITOR_SORT_DEFAULT_DIR[key];
          const second: SortMode['dir'] = first === 'asc' ? 'desc' : 'asc';
          return [first, second].map((dir) => (
            <option key={`${key}:${dir}`} value={`${key}:${dir}`}>
              {SORT_LABEL[key]}: {SORT_DIR_LABEL[key][dir]}
            </option>
          ));
        })}
      </select>
    </label>
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

// Column headers on the rows' grid, shown where the wide columns are. The
// status dot has no column of its own wide enough for a label, so the first
// header cell spans the lead and name columns and holds two column headers,
// Status then Name (each data row has a lead cell and a name cell, so the
// header and row cells still pair up for assistive technology).
function ListHeader({
  sort,
  onSortChange,
  ranking,
  selection,
}: {
  sort: SortMode;
  onSortChange: (sort: SortMode) => void;
  /** Status sort requested but results still loading: the order is by name for now. */
  ranking: boolean;
  /**
   * The "select all shown" box: revealed while the pointer is over the list
   * (the table is `group/list`), pinned once a selection exists.
   */
  selection?: { checked: boolean; indeterminate: boolean; pinned: boolean; onChange: () => void };
}) {
  const layout = useRowLayout();
  const header = (key: MonitorSortKey, label: string, align: 'left' | 'right' = 'right', title?: string, className = '') => (
    <SortableHeader
      as="div"
      label={label}
      sortKey={key}
      sort={sort}
      onSort={onSortChange}
      defaultDir={MONITOR_SORT_DEFAULT_DIR}
      align={align}
      title={title}
      className={className}
    />
  );
  return (
    <div
      role="row"
      className={`${layout.header} ${layout.cols} h-10 items-center gap-x-3 border-b border-divider px-4 text-xs font-medium text-slate-500`}
    >
      {/* Lead cell over the rows' checkbox slot; sort labels start at the
          name column, so "Name" sits over the names. */}
      <div role="columnheader" className="flex items-center">
        {selection ? (
          <Checkbox
            checked={selection.checked}
            indeterminate={selection.indeterminate}
            onChange={selection.onChange}
            aria-label={selection.checked ? 'Deselect all shown monitors' : 'Select all shown monitors'}
            className={
              selection.pinned || selection.checked
                ? 'opacity-100'
                : 'opacity-0 transition-opacity duration-fast ease-standard focus-within:opacity-100 group-hover/list:opacity-100'
            }
          />
        ) : (
          <span className="sr-only">Status</span>
        )}
      </div>
      <div role="none" className="flex min-w-0 items-center gap-5">
        {header('name', 'Name', 'left')}
        {header('status', ranking ? 'Status (ranking…)' : 'Status', 'left', 'Worst first: down, degraded, no data, up, maintenance, paused. A group ranks as its worst member.')}
      </div>
      <div role="columnheader" className={`${layout.target} min-w-0`}>Target</div>
      <div role="columnheader" className="whitespace-nowrap text-right" title="One bar per check, newest on the right">
        Last {RECENT_CHECKS} checks
      </div>
      {header('success', 'Success', 'right', `Share of the last ${RECENT_CHECKS} checks that passed`)}
      {header('latency', 'Latency', 'right', `Median of the successful checks among the last ${RECENT_CHECKS}`, `${layout.full} justify-end`)}
      {header('last_check', 'Last check', 'right', 'Stalest first, then most recent first', `${layout.full} justify-end whitespace-nowrap`)}
      <div role="columnheader">
        <span className="sr-only">Actions</span>
      </div>
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
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  // Deep-linked from the dashboard's unrouted warning (?routing=unrouted).
  const [unroutedOnly, setUnroutedOnly] = useState(false);
  const [sortMode, setSortMode] = useState<SortMode>(DEFAULT_MONITOR_SORT);
  const [selectedTags, setSelectedTags] = useState<Set<string>>(new Set());

  // Filters live in the URL so other pages can deep-link them (the
  // dashboard's ?status=down, ?tag=…, ?routing=unrouted) and a reload keeps
  // them. MonitorListUrlSync applies the URL on every navigation; state
  // changes are written back with replaceState, which Next mirrors into
  // useSearchParams, so the re-read then finds nothing to change.
  const [urlRead, setUrlRead] = useState(false);
  const applyUrlParams = useCallback((params: MonitorListParams) => {
    setStatusFilter(params.status);
    setUnroutedOnly(params.unroutedOnly);
    setSortMode((current) =>
      current.key === params.sort.key && current.dir === params.sort.dir ? current : params.sort
    );
    setSelectedTags((current) =>
      current.size === params.tags.length && params.tags.every((tag) => current.has(tag))
        ? current
        : new Set(params.tags)
    );
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
  // Group membership as ids (monitor_groups, from the results batch); the
  // member rows themselves are the list's own monitors, so they stay as
  // fresh as the list and a monitor in two groups is one object.
  const [memberIdsByGroup, setMemberIdsByGroup] = useState<Record<string, string[]>>({});
  const groupMembersMap = useMemo(() => {
    const byId = new Map(monitors.map((monitor) => [monitor.id, monitor]));
    const map: Record<string, Monitor[]> = {};
    Object.entries(memberIdsByGroup).forEach(([groupId, ids]) => {
      if (!byId.has(groupId)) return;
      map[groupId] = sortMonitorsByName(
        ids.map((id) => byId.get(id)).filter((member): member is Monitor => Boolean(member))
      );
    });
    return map;
  }, [monitors, memberIdsByGroup]);
  const { showToast } = useToast();
  const [pendingDelete, setPendingDelete] = useState<Monitor | null>(null);
  const [confirmBulkDelete, setConfirmBulkDelete] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [selectedMonitorIds, setSelectedMonitorIds] = useState<Set<string>>(new Set());
  // No separate select mode: a selection exists as soon as one box is ticked
  // (boxes reveal on row hover), and the floating action bar follows it.
  const selectionMode = selectedMonitorIds.size > 0;
  const [groupNameInput, setGroupNameInput] = useState('');
  const [targetGroupId, setTargetGroupId] = useState('');
  const [exporting, setExporting] = useState(false);
  const [showBulkAlerting, setShowBulkAlerting] = useState(false);
  const router = useRouter();

  // Below lg the detail panel is not rendered, so selecting a row would do
  // nothing visible — open the monitor's page instead.
  const selectMonitor = (id: string) => {
    // On touch screens (no hover), a tap toggles the row while a selection
    // exists, the usual mobile multi-select; with a pointer the boxes do it.
    if (selectionMode && typeof window !== 'undefined' && window.matchMedia('(hover: none)').matches) {
      toggleMonitorSelection(id);
      return;
    }
    if (typeof window !== 'undefined' && !window.matchMedia('(min-width: 1024px)').matches) {
      router.push(`/monitors/${id}`);
      return;
    }
    // The preview panel is opt-in: clicking the previewed row again closes it.
    setSelectedMonitorId((current) => (current === id ? null : id));
  };

  // ── Loading and live refresh ──
  //
  // The first load (and a manual refresh) fetches the list, then every
  // monitor's recent results and every group's member ids in one batched
  // round trip (fetchResultsBatches). Background ticks (useLiveRefresh: 30s
  // while visible, alert events, stream reconnects) refetch the list and
  // only the results that can have changed. A failed refresh keeps the rows
  // on screen.
  const monitorsRef = useRef<Monitor[]>([]);
  const fetchedAtRef = useRef(new Map<string, number>());
  const streamIdsRef = useRef(new Set<string>());
  const fullPassDone = useRef(false);

  const loadAllResults = useCallback(async (list: Monitor[]) => {
    const batch = await fetchResultsBatches(list.map((monitor) => monitor.id), RECENT_CHECKS);
    const now = Date.now();
    Object.keys(batch.results).forEach((id) => fetchedAtRef.current.set(id, now));
    const listed = new Set(list.map((monitor) => monitor.id));
    const keepFailed = <T,>(prev: Record<string, T>, next: Record<string, T>) => {
      batch.failed.forEach((id) => {
        if (id in prev) next[id] = prev[id];
      });
      return next;
    };
    // The exact maps replace the old ones (monitors that left the list drop
    // out); ids whose request failed keep what is on screen.
    setCheckResultsMap((prev) => keepFailed(prev, { ...batch.results }));
    setMemberIdsByGroup((prev) => {
      const next = keepFailed(prev, { ...batch.members });
      list.forEach((monitor) => {
        if (monitor.type === 'group' && !(monitor.id in next)) next[monitor.id] = [];
      });
      Object.keys(next).forEach((id) => {
        if (!listed.has(id)) delete next[id];
      });
      return next;
    });
    // Then say so: loadMonitors reports it (error callout or live status)
    // and, with the full pass not done, the next tick retries all of it.
    const failure = resultsBatchError(batch, list.length);
    if (failure) throw failure;
  }, []);

  // A background tick: results for monitors that changed state, that an
  // alert event named, or new to the list first, then those whose next
  // check is due since they were last fetched (stalest first), capped. The
  // batch returns member ids for any group among them, so a group that just
  // appeared (or changed state) gets its members too.
  const refreshChangedResults = useCallback(async (previous: Monitor[], list: Monitor[]) => {
    const before = new Map(previous.map((m) => [m.id, m]));
    const now = Date.now();
    const priority = new Set<string>(streamIdsRef.current);
    streamIdsRef.current.clear();
    list.forEach((m) => {
      const old = before.get(m.id);
      if (
        !old ||
        old.current_state !== m.current_state ||
        old.enabled !== m.enabled ||
        old.in_maintenance !== m.in_maintenance
      ) {
        priority.add(m.id);
      }
    });
    const due = list
      .filter((m) => m.enabled && !priority.has(m.id))
      .map((m) => {
        const intervalMs = Math.max(m.interval_seconds || 60, 10) * 1000;
        const since = now - (fetchedAtRef.current.get(m.id) ?? 0);
        return { id: m.id, overdue: since / intervalMs };
      })
      .filter((m) => m.overdue >= 1)
      .sort((a, b) => b.overdue - a.overdue)
      .map((m) => m.id);
    const ids = [...Array.from(priority).filter((id) => list.some((m) => m.id === id)), ...due].slice(
      0,
      RESULTS_PER_TICK
    );
    if (ids.length === 0) return;

    // Failed ids keep what is on screen (the live status reports the
    // failure) and stay due, so the next tick tries them again.
    const batch = await fetchResultsBatches(ids, RECENT_CHECKS);
    const fetchedAt = Date.now();
    Object.keys(batch.results).forEach((id) => fetchedAtRef.current.set(id, fetchedAt));
    if (Object.keys(batch.results).length > 0) setCheckResultsMap((prev) => ({ ...prev, ...batch.results }));
    if (Object.keys(batch.members).length > 0) setMemberIdsByGroup((prev) => ({ ...prev, ...batch.members }));
    const failure = resultsBatchError(batch, ids.length);
    if (failure) {
      batch.failed.forEach((id) => {
        if (priority.has(id)) streamIdsRef.current.add(id);
      });
      throw failure;
    }
  }, []);

  const loadMonitors = useCallback(
    async ({ silent }: LiveLoadOptions) => {
      try {
        if (!silent) setError('');
        const list = await getAllMonitors();
        const previous = monitorsRef.current;
        monitorsRef.current = list;
        setMonitors(list);
        // The list is on screen; results fill in behind it.
        setLoading(false);
        if (!silent || !fullPassDone.current) {
          await loadAllResults(list);
          fullPassDone.current = true;
        } else {
          await refreshChangedResults(previous, list);
        }
        setError('');
        setResultsReady(true);
        return true;
      } catch (err) {
        // Keep the rows already on screen. A background failure only shows
        // in the live status; a requested load also says why.
        if (!silent || monitorsRef.current.length === 0) {
          setError(err instanceof Error && err.message ? err.message : 'Failed to load monitors');
        }
        setLoading(false);
        setResultsReady(true);
        return false;
      }
    },
    [loadAllResults, refreshChangedResults]
  );

  // Alert events name the monitor whose results just moved; the stream
  // reload that useLiveRefresh debounces then refetches exactly those.
  const alertStream = useOptionalAlertEvents();
  const subscribeAlerts = alertStream?.subscribe;
  useEffect(() => {
    if (!subscribeAlerts) return;
    return subscribeAlerts((event) => {
      if (event.alert.monitor_id) streamIdsRef.current.add(event.alert.monitor_id);
    });
  }, [subscribeAlerts]);

  const live = useLiveRefresh(loadMonitors, {
    streamFilter: (event) => Boolean(event.alert.monitor_id),
  });
  const reloadAll = live.refresh;

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
    const target = monitors.find((m) => m.id === id);
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
      showToast({
        message: `${updated.name} ${updated.enabled ? 'resumed' : 'paused'}`,
        type: 'success',
        group: `toggle-${updated.id}`,
        action: { label: 'Undo', onClick: () => void handleToggleEnabled(updated) },
      });
    } catch (err: any) {
      showToast(err.message || 'Failed to update monitor', 'error');
    }
  };

  const handleSnooze = async (monitor: Monitor, durationMinutes: number) => {
    try {
      const window = await snoozeMonitor(monitor.id, { duration_minutes: durationMinutes });
      const updated: Monitor = { ...monitor, in_maintenance: true, maintenance_until: window.ends_at };
      setMonitors((prev) => prev.map((item) => (item.id === updated.id ? updated : item)));
      showToast(`Alerts snoozed until ${formatDateTime(window.ends_at)}`, 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to snooze monitor', 'error');
    }
  };

  // Shift-click extends from the last toggled row to this one, over the
  // top-level rows in their shown order (members inside an expanded group
  // toggle singly). The change event carries no modifier, so the shift state
  // is tracked from the pointer/key event that precedes it.
  const shiftHeldRef = useRef(false);
  const selectionAnchorRef = useRef<string | null>(null);
  const shownOrderRef = useRef<string[]>([]);
  useEffect(() => {
    const track = (e: PointerEvent | globalThis.KeyboardEvent) => {
      shiftHeldRef.current = e.shiftKey;
    };
    window.addEventListener('pointerdown', track, true);
    window.addEventListener('keydown', track, true);
    window.addEventListener('keyup', track, true);
    return () => {
      window.removeEventListener('pointerdown', track, true);
      window.removeEventListener('keydown', track, true);
      window.removeEventListener('keyup', track, true);
    };
  }, []);

  const toggleMonitorSelection = (id: string) => {
    const anchor = selectionAnchorRef.current;
    const order = shownOrderRef.current;
    const from = anchor ? order.indexOf(anchor) : -1;
    const to = order.indexOf(id);
    const range = shiftHeldRef.current && from >= 0 && to >= 0 && from !== to;
    selectionAnchorRef.current = id;
    setSelectedMonitorIds((prev) => {
      const next = new Set(prev);
      const select = !prev.has(id);
      const ids = range ? order.slice(Math.min(from, to), Math.max(from, to) + 1) : [id];
      ids.forEach((rowId) => (select ? next.add(rowId) : next.delete(rowId)));
      return next;
    });
  };

  const clearSelection = () => {
    setSelectedMonitorIds(new Set());
    selectionAnchorRef.current = null;
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
    return Array.from(tagSet).sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base', numeric: true }));
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
  // `ignore` leaves one filter out: a filter popover counts its options
  // against everything else that is set.
  const monitorMatchesFilters = (monitor: Monitor, ignore?: 'type' | 'tags') => {
    const matchesSearch =
      !query ||
      monitor.name.toLowerCase().includes(query) ||
      Boolean(monitorTarget(monitor)?.toLowerCase().includes(query)) ||
      Boolean(monitor.tags?.some((tag) => tag.toLowerCase().includes(query)));
    const matchesType = ignore === 'type' || selectedTypes.size === 0 || selectedTypes.has(monitor.type);
    const status = statusOf(monitor);
    const matchesStatus = statusFilter === 'all' ||
      (statusFilter === 'up' && status === 'up') ||
      (statusFilter === 'down' && (status === 'down' || status === 'degraded')) ||
      (statusFilter === 'paused' && status === 'paused') ||
      (statusFilter === 'maintenance' && status === 'maintenance');
    const matchesTags = ignore === 'tags' || selectedTags.size === 0 ||
      Boolean(monitor.tags && monitor.tags.some((tag) => selectedTags.has(tag)));
    const matchesRouting =
      !unroutedOnly || (monitor.enabled && monitor.alert_routing?.reachable === false);
    return matchesSearch && matchesType && matchesStatus && matchesTags && matchesRouting;
  };

  // Options for the Type and Tags filter popovers. Each count is the
  // monitors that option would add under the other filters (Datadog,
  // GitHub), so "global 11" never leads to an empty list; the order stays
  // the workspace order so rows do not move while ticking. A URL-selected
  // tag no monitor carries shows 0.
  const typeOptions: FilterOption[] = (() => {
    const counts = new Map<string, number>();
    monitors.forEach((monitor) => {
      if (monitorMatchesFilters(monitor, 'type')) counts.set(monitor.type, (counts.get(monitor.type) || 0) + 1);
    });
    return typeCounts.map(([type]) => ({ value: type, label: monitorTypeLabel(type), count: counts.get(type) ?? 0 }));
  })();
  const tagOptions: FilterOption[] = (() => {
    const counts = new Map<string, number>();
    monitors.forEach((monitor) => {
      if (monitorMatchesFilters(monitor, 'tags')) monitor.tags?.forEach((tag) => counts.set(tag, (counts.get(tag) || 0) + 1));
    });
    return allTags.map((tag) => ({ value: tag, label: tag, count: counts.get(tag) ?? 0, dotClassName: getTagColor(tag).dot }));
  })();

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

  // One sort over groups and monitors together (lib/monitor-list.ts): a
  // group ranks by its worst member under the status sort and is never
  // pinned. Until the first results pass lands every monitor reads "no
  // data", so a results-based sort keeps name order (monitors arrive
  // name-sorted) and re-ranks once instead of jumping with every chunk.
  const sortContext: MonitorSortContext = {
    statusOf,
    resultsOf: (monitor) => checkResultsMap[monitor.id] || [],
    membersOf: (groupId) => groupMembersMap[groupId] || [],
  };
  const ranking = !resultsReady && sortMode.key !== 'name';
  const orderMonitors = (items: Monitor[]) =>
    ranking ? sortMonitorsByName(items) : sortMonitorList(items, sortMode, sortContext);

  // Filter monitors (members of a group only show under their group), then
  // sort the whole filtered set.
  const topLevel = monitors.filter((monitor) => {
    if (groupMemberIds.has(monitor.id)) return false;
    if (monitorMatchesFilters(monitor)) return true;
    return monitor.type === 'group' && groupHasMatchingMember(monitor.id);
  });
  const filteredMonitors = orderMonitors(topLevel);
  shownOrderRef.current = filteredMonitors.map((m) => m.id);
  const hasGroups = filteredMonitors.some((m) => m.type === 'group');

  // Escape drops the selection (unless a field or dialog has the key).
  useEffect(() => {
    if (!selectionMode) return;
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented) return;
      const target = e.target as HTMLElement | null;
      // A text field or dialog keeps its own Escape; the row boxes do not.
      if (target?.closest('input:not([type="checkbox"]), textarea, select, [role="dialog"]')) return;
      clearSelection();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [selectionMode]);

  const selectedMonitor = monitors.find((m) => m.id === selectedMonitorId) || null;
  const selectedMonitors = monitors.filter((m) => selectedMonitorIds.has(m.id));
  const availableGroups = monitors
    .filter((m) => m.type === 'group' && !selectedMonitorIds.has(m.id))
    .sort((a, b) => a.name.localeCompare(b.name));
  const allFilteredSelected = filteredMonitors.length > 0 && filteredMonitors.every((m) => selectedMonitorIds.has(m.id));
  const someFilteredSelected = !allFilteredSelected && filteredMonitors.some((m) => selectedMonitorIds.has(m.id));
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
      await reloadAll();
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
      await reloadAll();
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

  // Rows whose status moved since the last refresh flash once. The baseline
  // is the first complete results pass, so loading never flashes.
  const statusRows = useMemo(
    () =>
      resultsReady
        ? monitors.map((m) => ({ id: m.id, name: m.name, status: getEffectiveMonitorStatus(m, checkResultsMap[m.id] || []) }))
        : null,
    [monitors, checkResultsMap, resultsReady]
  );
  const rowFlash = useRowFlash(
    statusRows,
    (row) => row.id,
    (row) => row.status
  );

  // Screen-reader news, politely: status changes the flash shows, and the
  // shown count after a filter or sort change (debounced while typing).
  const [statusNews, setStatusNews] = useState('');
  const announced = useRef(new Set<string>());
  useEffect(() => {
    const fresh = Array.from(rowFlash.flashing).filter((id) => !announced.current.has(id));
    announced.current = new Set(rowFlash.flashing);
    if (fresh.length === 0 || !statusRows) return;
    const byId = new Map(statusRows.map((row) => [row.id, row]));
    const parts = fresh
      .map((id) => byId.get(id))
      .filter((row): row is NonNullable<typeof row> => Boolean(row))
      .map((row) => `${row.name} is now ${MONITOR_STATUS_LABEL[row.status].toLowerCase()}`);
    if (parts.length > 0) setStatusNews(parts.length > 3 ? `${parts.length} monitors changed status` : parts.join('. '));
  }, [rowFlash.flashing, statusRows]);

  const shownCount = filteredMonitors.length;
  const [listNews, setListNews] = useState('');
  const sortKeyDir = `${sortMode.key}:${sortMode.dir}`;
  useEffect(() => {
    if (!resultsReady) return;
    const timer = window.setTimeout(() => {
      setListNews(`${shownCount} ${shownCount === 1 ? 'row' : 'rows'} shown, sorted by ${sortDescription(sortMode)}`);
    }, 600);
    return () => window.clearTimeout(timer);
    // sortMode is read through its key:dir string.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shownCount, sortKeyDir, resultsReady, searchTerm, statusFilter, selectedTypes, selectedTags, unroutedOnly]);

  const rowLayout = ROW_LAYOUTS[selectedMonitor ? 'panel' : 'list'];
  const listStyle = leadStyle(canWrite, hasGroups);
  // On phones the chip rows scroll sideways instead of wrapping into a wall.
  const scrollRow =
    '-mx-4 flex flex-nowrap items-center gap-1.5 overflow-x-auto px-4 [scrollbar-width:none] sm:-mx-6 sm:px-6 lg:mx-0 lg:flex-wrap lg:overflow-visible lg:px-0';

  return (
    <Page width="data" className={canWrite && selectionMode ? 'pb-20' : undefined}>
      <Suspense fallback={null}>
        <MonitorListUrlSync onParams={applyUrlParams} />
      </Suspense>
      <p role="status" className="sr-only">{listNews}</p>
      <p role="status" className="sr-only">{statusNews}</p>
      {/* Header */}
      <PageHeader
        title="Monitors"
        action={
          <div className="flex items-center gap-2">
            {!isEmptyWorkspace && (
              <LiveStatus
                updatedAt={live.updatedAt}
                refreshing={live.refreshing}
                failed={live.failed && monitors.length > 0}
                onRefresh={() => void reloadAll()}
                refreshLabel="Refresh monitors"
                className="mr-1 hidden md:inline-flex"
              />
            )}
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
          {/* Grows to 16rem but gives width back (down to 9rem) before the
              row wraps, so setting a Type/Tags filter (its chip gains a
              value and a ×) does not push the list down at laptop widths. */}
          <div className="relative w-full lg:w-auto lg:min-w-0 lg:max-w-64 lg:flex-[1_1_9rem]">
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

          {/* A selected Type/Tags chip shows a short value or "Tags ⓝ", so its
              width stays bounded; the search field above absorbs it. */}
          <div className={`${scrollRow} lg:min-w-max lg:flex-none lg:flex-nowrap`}>
            <FilterPopover
              label="Type"
              noun="type"
              icon={<Layers strokeWidth={1.75} />}
              options={typeOptions}
              selected={selectedTypes}
              onChange={setSelectedTypes}
            />

            {allTags.length > 0 && (
              <FilterPopover
                label="Tags"
                noun="tag"
                icon={<Tag strokeWidth={1.75} />}
                options={tagOptions}
                selected={selectedTags}
                onChange={setSelectedTags}
              />
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
                <NarrowSortSelect sort={sortMode} onSortChange={setSortMode} />
              </RowLayoutContext.Provider>
            )}
          </div>
        </div>
      </div>
      )}

      {/* Error */}
      {error && (
        <Callout
          tone="danger"
          action={
            <Button variant="danger" size="xs" onClick={() => void reloadAll()} loading={live.loading}>
              Retry
            </Button>
          }
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
        <RowFlashContext.Provider value={rowFlash.flashClass}>
        <div
          className={`grid gap-6 ${
            selectedMonitor
              ? 'lg:grid-cols-[minmax(0,1fr)_320px] 3xl:grid-cols-[minmax(0,1fr)_400px] 4xl:grid-cols-[minmax(0,1fr)_480px]'
              : ''
          }`}
        >
          {/* Monitor List */}
          <div className="glass min-w-0 self-start overflow-hidden" style={listStyle}>
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
              <SelectableContext.Provider value={canWrite}>
              <div
                role="table"
                aria-label="Monitors"
                aria-busy={ranking || undefined}
                onKeyDown={handleRowArrowKeys}
                className="group/list"
              >
              <ListHeader
                sort={sortMode}
                onSortChange={setSortMode}
                ranking={ranking}
                selection={
                  canWrite
                    ? {
                        checked: allFilteredSelected,
                        indeterminate: someFilteredSelected,
                        pinned: selectionMode,
                        onChange: toggleSelectAllFiltered,
                      }
                    : undefined
                }
              />
              <div role="rowgroup" className="divide-y divide-divider">
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
              </div>
              </SelectableContext.Provider>
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
        </RowFlashContext.Provider>
        </RowLayoutContext.Provider>
      )}

      {/* Bulk actions: floats over the page while a selection exists, so the
          list never shifts. Portaled (fixed inside a .glass would be offset). */}
      {canWrite && selectionMode && typeof document !== 'undefined' &&
        createPortal(
          <div
            data-floating-bar
            className="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex justify-center px-4 transition-[left] duration-slow ease-emphasized sm:bottom-6 md:left-[var(--sidebar-offset)]"
          >
            <div
              role="toolbar"
              aria-label="Bulk actions"
              className="glass-overlay pointer-events-auto flex max-w-full animate-toast-in flex-wrap items-center gap-2 rounded-xl px-3 py-2"
            >
              <span className="px-1 text-ui font-medium tabular-nums text-white" aria-live="polite">
                {selectedMonitorIds.size} selected
              </span>
              <Button variant="ghost" size="xs" onClick={toggleSelectAllFiltered}>
                {allFilteredSelected ? 'Deselect all' : `Select all ${filteredMonitors.length}`}
              </Button>

              <div className="h-4 w-px bg-divider" />

              {/* Group creation */}
              <div className="flex items-center gap-1.5">
                <input
                  type="text"
                  placeholder="New group name…"
                  aria-label="New group name"
                  value={groupNameInput}
                  onChange={(e) => setGroupNameInput(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && canCreateGroup) handleCreateGroup();
                  }}
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

              <Button variant="ghost" size="xs" icon={<Bell strokeWidth={1.75} />} onClick={() => setShowBulkAlerting(true)}>
                Edit alerting…
              </Button>
              <Button variant="danger" size="xs" icon={<Trash2 strokeWidth={1.75} />} onClick={() => setConfirmBulkDelete(true)}>
                Delete
              </Button>

              <div className="h-4 w-px bg-divider" />

              <IconButton label="Clear selection (Esc)" icon={<X strokeWidth={1.75} />} onClick={clearSelection} size="sm" />
            </div>
          </div>,
          document.body
        )}

      {showBulkAlerting && (
        <BulkAlertingModal
          monitorIds={Array.from(selectedMonitorIds)}
          onDone={() => {
            setShowBulkAlerting(false);
            setSelectedMonitorIds(new Set());
            showToast('Alerting settings updated', 'success');
            void reloadAll();
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
    </Page>
  );
}
