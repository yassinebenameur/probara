'use client';

import { KeyboardEvent, ReactNode, useState } from 'react';
import Link from 'next/link';
import { Bell, BellOff, Check, CircleCheck, Network, PencilLine, Tag, UserCheck } from 'lucide-react';
import { Alert } from '@/lib/types';
import { acknowledgeAlert, labelAlertTriage, resolveAlert } from '@/lib/api';
import { labelErrorMessage } from '@/lib/ai-labels';
import { buildTriageLabelRequest, triageMenuCopy, triagePresentation } from '@/lib/ai-triage';
import { formatDateTime, formatDateTimeFull, pluralize } from '@/lib/format';
import { formatAlertValue, formatSeriesLabel } from '@/lib/metrics';
import { shortFailureReason } from '@/lib/monitor-utils';
import { ALERT_STATUS_LABEL, TONE_DOT, alertStatusTone } from '@/lib/status-tones';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import Button from '@/components/ui/Button';
import Checkbox from '@/components/ui/Checkbox';
import SelectCheckbox from '@/components/ui/SelectCheckbox';
import EmptyState from '@/components/ui/EmptyState';
import Pill from '@/components/ui/Pill';
import RowMenu, { RowMenuItem } from '@/components/ui/RowMenu';
import TriagePill from '@/components/alerts/TriagePill';
import TriageLabelDialog from '@/components/alerts/TriageLabelDialog';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { useToast } from '@/components/ui/ToastProvider';
import { ElapsedTime } from '@/components/ui/RelativeTime';
import SortableHeader from '@/components/ui/SortableHeader';
import { FOCUS_RING } from '@/components/ui/focus';
import { useOptionalAlertEvents } from '@/components/alerts/AlertStreamProvider';
import type { AlertSelection } from '@/components/alerts/useAlertSelection';
import { sortRows, type SortState, type SortValue } from '@/lib/sort';
import {
  ALERT_SORT_DEFAULT_DIR,
  SHORTCUT_EXEMPT,
  rowShortcut,
  shortcutEffect,
  type AlertSortKey,
} from '@/lib/alert-list';
import { flashStyle } from '@/lib/useRowFlash';

// The row's one primary action (Acknowledge or Resolve) sits in a fixed
// slot so it lines up down the column; an icon square below sm.
const PRIMARY_SLOT = 'shrink-0 max-sm:w-8 max-sm:px-0 sm:w-[7.5rem]';

const STATUS_ORDER: Record<string, number> = { active: 0, acknowledged: 1, resolved: 2 };

// A row's selection box keeps its slot (nothing shifts when a selection
// starts) but shows only on row hover or keyboard focus, always once a
// selection exists, and always on touch screens, which have no hover.
const REVEAL_ON_ROW_HOVER =
  'opacity-0 transition-opacity duration-fast ease-standard focus-within:opacity-100 group-hover/row:opacity-100 [@media(hover:none)]:opacity-100';
const REVEAL_ON_LIST_HOVER =
  'opacity-0 transition-opacity duration-fast ease-standard focus-within:opacity-100 group-hover/list:opacity-100 [@media(hover:none)]:opacity-100';

function alertSubject(alert: Alert): string {
  return alert.kind === 'mesh_edge'
    ? `${alert.source_location_name || ''} → ${alert.target_location_name || ''}`
    : alert.monitor_name || alert.monitor_id || '';
}

/**
 * Sort a full alert list by a column (sort before paging). Ties read newest
 * first, so the default status sort is the operator's queue.
 */
export function sortAlerts(alerts: readonly Alert[], sort: SortState<AlertSortKey>, now = Date.now()): Alert[] {
  const value: Record<AlertSortKey, (alert: Alert) => SortValue> = {
    status: (alert) => STATUS_ORDER[alert.status] ?? null,
    monitor: alertSubject,
    started: (alert) => Date.parse(alert.triggered_at),
    // An ongoing alert's duration runs to now.
    duration: (alert) =>
      (alert.resolved_at ? Date.parse(alert.resolved_at) : now) - Date.parse(alert.triggered_at),
  };
  return sortRows(alerts, value[sort.key], sort.dir, (alert) => -Date.parse(alert.triggered_at));
}

interface AlertTableProps {
  alerts: Alert[];
  onAlertUpdate?: (alert: Alert) => void;
  /**
   * A triage label was saved for this alert. The owner flips the row's
   * `labelled` flag on its current list (`markTriageLabelled`); passing an id
   * rather than a row keeps a live update made meanwhile.
   */
  onTriageLabelled?: (alertId: string) => void;
  /** A fetch is in flight: skeleton on the first load, dimmed rows after. */
  loading?: boolean;
  emptyTitle?: string;
  /**
   * Column sorting. The owner applies it: sortAlerts before paging where it
   * holds the whole list, the API's sort (ALERT_SORT_COLUMN) where it pages
   * on the server. Left out, the header is static and the rows read newest
   * first.
   */
  sort?: SortState<AlertSortKey>;
  onSort?: (next: SortState<AlertSortKey>) => void;
  /**
   * False under a one-status filter, where Status orders nothing: its header
   * is then plain text (lib/alert-list effectiveAlertSort).
   */
  statusSortable?: boolean;
  /**
   * Row selection for bulk triage (useAlertSelection over the open rows on
   * screen). Shown to writers only; resolved rows keep an empty slot.
   */
  selection?: AlertSelection;
  /** useRowFlash().flashClass: rows whose status just changed flash in their new tone. */
  flashClass?: (id: string) => string;
}

const DETAIL = 'mt-0.5 text-xs tabular-nums text-slate-400';
// The key cap the sidebar's tooltips use (components/ui/Tooltip.tsx).
const KBD = 'rounded border border-edge px-1 font-mono text-2xs leading-4 text-slate-400';
const CHIP = 'inline-flex max-w-full items-center gap-1 rounded-full px-2 py-0.5 text-2xs font-medium';

function KindTag({ title, children }: { title: string; children: ReactNode }) {
  return (
    <span className="ml-2 align-middle" title={title}>
      <Pill size="xs">{children}</Pill>
    </span>
  );
}

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback;
}

export default function AlertTable({
  alerts,
  onAlertUpdate,
  onTriageLabelled,
  loading,
  emptyTitle = 'No alerts found',
  sort,
  onSort,
  statusSortable = true,
  selection,
  flashClass,
}: AlertTableProps) {
  const [processing, setProcessing] = useState<{ id: string; action: 'ack' | 'resolve' | 'label' } | null>(null);
  const [labelling, setLabelling] = useState<{ alert: Alert; mode: 'prompt' | 'correct' } | null>(null);
  // Screen-reader echo of the user's own ack/resolve; sighted users see the
  // row change (and flash), so there is no success toast. `seq` alternates a
  // trailing no-break space so a repeat of the same text still changes the
  // live region and is read again.
  const [announcement, setAnnouncementState] = useState({ text: '', seq: 0 });
  const setAnnouncement = (text: string) => setAnnouncementState((prev) => ({ text, seq: prev.seq + 1 }));
  const canWrite = useCanWrite();
  const { showToast } = useToast();
  const stream = useOptionalAlertEvents();

  const handleAcknowledge = async (alertId: string) => {
    try {
      setProcessing({ id: alertId, action: 'ack' });
      // The stream echoes this change; marking it keeps that echo silent.
      stream?.markLocalAction(alertId);
      const updated = await acknowledgeAlert(alertId);
      onAlertUpdate?.(updated);
      setAnnouncement(`Alert for ${alertSubject(updated)} acknowledged`);
    } catch (err) {
      // The (assertive) error toast is the announcement; drop the stale one.
      setAnnouncement('');
      showToast(errorMessage(err, 'Failed to acknowledge alert'), 'error');
    } finally {
      setProcessing(null);
    }
  };

  const handleResolve = async (alertId: string) => {
    try {
      setProcessing({ id: alertId, action: 'resolve' });
      stream?.markLocalAction(alertId);
      const updated = await resolveAlert(alertId);
      onAlertUpdate?.(updated);
      setAnnouncement(`Alert for ${alertSubject(updated)} resolved`);
    } catch (err) {
      setAnnouncement('');
      showToast(errorMessage(err, 'Failed to resolve alert'), 'error');
    } finally {
      setProcessing(null);
    }
  };

  // A / R act on the row that has focus (its link, buttons or box), so the
  // single-key shortcuts are scoped to the list as WCAG 2.1.4 allows: they
  // never fire while typing, and do nothing outside an alert row. Arrow keys
  // move between the rows' subject links.
  // The guard (exempt targets, modifiers) is lib/alert-list rowShortcut.
  const handleRowKeyDown = (event: KeyboardEvent<HTMLTableRowElement>, alert: Alert) => {
    const target = event.target as HTMLElement;
    const shortcut = rowShortcut(event, target.isContentEditable || target.closest(SHORTCUT_EXEMPT) !== null);
    if (!shortcut) return;

    if (shortcut === 'next' || shortcut === 'previous') {
      const links = Array.from(event.currentTarget.closest('tbody')?.querySelectorAll<HTMLElement>('.alert-row-link') ?? []);
      const current = event.currentTarget.querySelector<HTMLElement>('.alert-row-link');
      const next = links[links.indexOf(current as HTMLElement) + (shortcut === 'next' ? 1 : -1)];
      if (!next) return;
      event.preventDefault();
      next.focus();
      return;
    }

    if (!canWrite) return;
    event.preventDefault();
    if (event.repeat || processing?.id === alert.id) return;
    const effect = shortcutEffect(shortcut, alert.status);
    if (effect === 'apply') {
      void (shortcut === 'acknowledge' ? handleAcknowledge(alert.id) : handleResolve(alert.id));
      return;
    }
    // A press that changes nothing says so on screen too (the toast region
    // announces it), so a sighted keyboard user is not left guessing.
    setAnnouncement('');
    showToast(
      `Alert for ${alertSubject(alert)} is already ${effect === 'already_resolved' ? 'resolved' : 'acknowledged'}`,
      'info'
    );
  };

  // Pills reflect the last fetch only; a saved label flips this row's
  // `labelled` flag rather than refetching the page. Only the id goes up:
  // this closure's `alerts` is the render the action started in, so sending
  // a row from it would overwrite a live update that landed meanwhile.
  const handleConfirmTriage = async (alert: Alert) => {
    try {
      setProcessing({ id: alert.id, action: 'label' });
      await labelAlertTriage(alert.id, buildTriageLabelRequest('confirmed'));
      onTriageLabelled?.(alert.id);
      showToast('Label saved', 'success');
    } catch (err) {
      showToast(labelErrorMessage(err, 'Could not save the label'), 'error');
    } finally {
      setProcessing(null);
    }
  };

  // AILabelControl already toasts "Label saved".
  const handleDialogLabelled = () => {
    if (labelling) onTriageLabelled?.(labelling.alert.id);
    setLabelling(null);
  };

  // The skeleton is for the first load only; a refetch keeps the rows on
  // screen (dimmed) so the page neither jumps nor loses its scroll.
  if (alerts.length === 0) {
    if (loading) return <TableSkeleton rows={5} columns={3} label="Loading alerts" />;
    return (
      <EmptyState
        icon={<Bell strokeWidth={1.75} />}
        title={emptyTitle}
        description="Alerts appear here when a monitor goes down."
      />
    );
  }

  // No actions column when nothing listed can be acted on (the Resolved
  // view without labelable triage): empty slots would only squeeze the
  // monitor column. Each slot shows only when some row uses it, and rows
  // without that action keep an empty slot so the columns stay aligned.
  const anyOpen = alerts.some((alert) => alert.status !== 'resolved');
  const anyLabelable = alerts.some((alert) => alert.triage?.labelable);
  const showActions = canWrite && (anyOpen || anyLabelable);
  // The selection slot stays whenever selection is on, even in a view with
  // nothing open to select, so switching the status filter never shifts the
  // columns.
  const selectable = canWrite && selection !== undefined;
  const selectionPinned = (selection?.selected.size ?? 0) > 0;
  const selectHeader = selectable && selection && (
    <th className="w-px !pr-0">
      {anyOpen ? (
        <Checkbox
          checked={selection.allSelected}
          indeterminate={selection.someSelected}
          onChange={selection.toggleAll}
          aria-label={selection.allSelected ? 'Deselect all open alerts shown' : 'Select all open alerts shown'}
          className={selectionPinned || selection.allSelected ? 'opacity-100' : REVEAL_ON_LIST_HOVER}
        />
      ) : (
        <span className="block h-4 w-4" aria-hidden="true" />
      )}
    </th>
  );


  return (
    // The dim waits 300ms, so a quick refresh never blinks the table.
    <div
      className={`group/table glass overflow-hidden transition-opacity duration-base ${
        loading ? 'pointer-events-none opacity-60 delay-300' : 'delay-0'
      }`}
      aria-busy={loading || undefined}
    >
      <p role="status" className="sr-only">
        {announcement.text && `${announcement.text}${announcement.seq % 2 ? '\u00a0' : ''}`}
      </p>
      <div className="group/list overflow-x-auto">
        <table className="data-table text-ui">
          <thead>
            {/* Fixed-width labels (Status fits "Acknowledged") set each
                column's minimum, so switching the status filter never
                shifts the columns; Monitor (w-full) takes the rest. */}
            {sort && onSort ? (
              <tr>
                {selectHeader}
                {statusSortable ? (
                  <SortableHeader
                    label={<span className="max-sm:sr-only">Status</span>}
                    sortKey="status"
                    sort={sort}
                    onSort={onSort}
                    defaultDir={ALERT_SORT_DEFAULT_DIR}
                    title={sort.key === 'status' && sort.dir === 'desc' ? 'Resolved first' : 'Active first'}
                    className="max-sm:w-8 sm:w-36"
                  />
                ) : (
                  <th className="max-sm:w-8 sm:w-36">
                    <span className="max-sm:sr-only">Status</span>
                  </th>
                )}
                <SortableHeader
                  label="Monitor"
                  sortKey="monitor"
                  sort={sort}
                  onSort={onSort}
                  defaultDir={ALERT_SORT_DEFAULT_DIR}
                  className="w-full"
                />
                <SortableHeader
                  label="Started"
                  sortKey="started"
                  sort={sort}
                  onSort={onSort}
                  defaultDir={ALERT_SORT_DEFAULT_DIR}
                  title={sort.key === 'started' && sort.dir === 'asc' ? 'Oldest first' : 'Newest first'}
                  className="hidden md:table-cell md:w-36"
                />
                <SortableHeader
                  label="Duration"
                  sortKey="duration"
                  sort={sort}
                  onSort={onSort}
                  defaultDir={ALERT_SORT_DEFAULT_DIR}
                  title={sort.key === 'duration' && sort.dir === 'asc' ? 'Shortest first' : 'Longest first'}
                  className="hidden md:table-cell md:w-44"
                />
                {showActions && (
                  <th>
                    <span className="sr-only">Actions</span>
                  </th>
                )}
              </tr>
            ) : (
            <tr>
              {selectHeader}
              <th className="max-sm:w-8">
                <span className="max-sm:sr-only sm:inline-block sm:w-28">Status</span>
              </th>
              <th className="w-full">Monitor</th>
              {/* Server-paged: the API lists newest first. */}
              <th className="hidden md:table-cell" title="Newest first">
                <span className="inline-block w-24">Started</span>
              </th>
              <th className="hidden md:table-cell">
                <span className="inline-block w-32">Duration</span>
              </th>
              {showActions && (
                <th>
                  <span className="sr-only">Actions</span>
                </th>
              )}
            </tr>
            )}
          </thead>
          <tbody>
            {alerts.map((alert) => (
              <tr
                key={alert.id}
                // Checked rows fill like the monitors list (never orange);
                // the focused row is the one A / R act on, so it shows.
                className={`group/row has-[:focus-visible]:bg-well ${
                  selection?.selected.has(alert.id) ? '!bg-well-strong' : ''
                } ${flashClass?.(alert.id) || ''}`}
                style={flashClass?.(alert.id) ? flashStyle(alertStatusTone(alert.status)) : undefined}
                onKeyDown={(event) => handleRowKeyDown(event, alert)}
              >
                {selectable && selection && (
                  <td className="w-px !pr-0">
                    {alert.status !== 'resolved' ? (
                      <SelectCheckbox
                        checked={selection.selected.has(alert.id)}
                        onChange={() => selection.toggle(alert.id)}
                        label={`Select alert for ${alertSubject(alert)}`}
                        className={selectionPinned ? 'opacity-100' : REVEAL_ON_ROW_HOVER}
                      />
                    ) : (
                      <span className="block h-4 w-4" aria-hidden="true" />
                    )}
                  </td>
                )}
                <td className="max-sm:pr-0">
                  <span className="max-sm:hidden">
                    <Pill size="sm" dot tone={alertStatusTone(alert.status)}>{ALERT_STATUS_LABEL[alert.status]}</Pill>
                  </span>
                  <span
                    className={`block h-2 w-2 rounded-full sm:hidden ${TONE_DOT[alertStatusTone(alert.status)]}`}
                    title={ALERT_STATUS_LABEL[alert.status]}
                  >
                    <span className="sr-only">{ALERT_STATUS_LABEL[alert.status]}</span>
                  </span>
                </td>
                <td className="max-w-0">
                  {alert.kind === 'mesh_edge' ? (
                    <Link
                      href="/locations?tab=mesh"
                      className={`alert-row-link inline-flex items-center gap-1.5 rounded-sm text-sm font-medium text-white transition-colors hover:text-cyan-300 ${FOCUS_RING}`}
                      title="Inter-location mesh path down — view the connectivity matrix"
                    >
                      <Network className="h-3.5 w-3.5 text-slate-400" strokeWidth={1.75} />
                      {alert.source_location_name || 'unknown'} → {alert.target_location_name || 'unknown'}
                    </Link>
                  ) : (
                    <Link
                      href={`/monitors/${alert.monitor_id}`}
                      className={`alert-row-link rounded-sm text-sm font-medium text-white transition-colors hover:text-cyan-300 ${FOCUS_RING}`}
                    >
                      {alert.monitor_name || alert.monitor_id}
                    </Link>
                  )}
                  {alert.kind === 'mesh_edge' && (
                    <KindTag title="Directed inter-location connectivity path">mesh</KindTag>
                  )}
                  {alert.kind === 'latency_anomaly' && (
                    <KindTag title="Latency degraded relative to baseline">latency</KindTag>
                  )}
                  {alert.kind === 'latency_anomaly' && alert.observed_latency_ms != null && alert.baseline_latency_ms != null && (
                    <p className={DETAIL}>
                      {Math.round(alert.observed_latency_ms)} ms vs ~{Math.round(alert.baseline_latency_ms)} ms baseline
                      {alert.anomaly_score != null && ` (${alert.anomaly_score.toFixed(1)}σ)`}
                    </p>
                  )}
                  {alert.kind === 'host_metric' && (
                    <KindTag title="Host metric breached its configured threshold">
                      {formatSeriesLabel(alert.metric_name)}
                    </KindTag>
                  )}
                  {alert.kind === 'host_metric' && alert.metric_value != null && (
                    <p className={DETAIL}>
                      {formatAlertValue(alert.metric_name, alert.metric_value)}
                      {alert.threshold_value != null &&
                        ` (threshold ${formatAlertValue(alert.metric_name, alert.threshold_value)})`}
                    </p>
                  )}
                  {alert.kind === 'tls_expiry' && (
                    <KindTag title="TLS certificate is inside its expiry warning window — the endpoint itself is still up">
                      certificate
                    </KindTag>
                  )}
                  {alert.kind === 'tls_expiry' && alert.metric_value != null && (
                    <p className={DETAIL}>
                      expires in {Math.round(alert.metric_value)}d
                      {alert.threshold_value != null && ` (threshold ${Math.round(alert.threshold_value)}d)`}
                    </p>
                  )}
                  {alert.last_error && (
                    <p className="mt-0.5 truncate text-xs text-slate-400" title={alert.last_error}>
                      {shortFailureReason(alert.last_error) || alert.last_error}
                    </p>
                  )}
                  <p className={`${DETAIL} flex flex-wrap gap-x-3 md:hidden`}>
                    <ElapsedTime start={alert.triggered_at} end={alert.resolved_at} />
                    <span>{alert.resolved_at ? `ended ${formatDateTime(alert.resolved_at)}` : 'ongoing'}</span>
                    <span>{pluralize(alert.failure_count, 'recent failure')}</span>
                  </p>
                  {((alert.root_cause_monitor_name && alert.root_cause_monitor_id !== alert.monitor_id) ||
                    (alert.suppression_reason === 'dependency' && !alert.root_cause_monitor_name) ||
                    (alert.impacted_count ?? 0) > 0 ||
                    triagePresentation(alert.triage).visible) && (
                    <div className="mt-1.5 flex flex-wrap items-center gap-1">
                      {alert.root_cause_monitor_name && alert.root_cause_monitor_id !== alert.monitor_id && (
                        <Link
                          href={`/monitors/${alert.root_cause_monitor_id}`}
                          className={`${CHIP} min-w-0 transition-colors ${FOCUS_RING} ${
                            alert.suppression_reason === 'dependency'
                              ? 'bg-well-hover text-slate-400 hover:text-slate-200'
                              : 'bg-amber-500/[0.15] text-amber-300 hover:text-amber-200'
                          }`}
                          title={
                            alert.suppression_reason === 'dependency'
                              ? `Notifications suppressed: caused by ${alert.root_cause_monitor_name}`
                              : `Likely caused by ${alert.root_cause_monitor_name}`
                          }
                        >
                          {alert.suppression_reason === 'dependency' && <BellOff className="h-3 w-3 shrink-0" strokeWidth={1.75} />}
                          <span className="truncate">
                            {alert.suppression_reason === 'dependency' ? 'Suppressed, caused by' : 'Likely caused by'} {alert.root_cause_monitor_name}
                          </span>
                        </Link>
                      )}
                      {alert.suppression_reason === 'dependency' && !alert.root_cause_monitor_name && (
                        <span
                          className={`${CHIP} bg-well-hover text-slate-400`}
                          title="Notifications suppressed: the upstream dependency recovered moments ago; this pages if still down after the grace period"
                        >
                          <BellOff className="h-3 w-3 shrink-0" strokeWidth={1.75} />
                          Suppressed, upstream just recovered
                        </span>
                      )}
                      {(alert.impacted_count ?? 0) > 0 && (
                        <span
                          className={`${CHIP} bg-amber-500/[0.15] text-amber-300`}
                          title="Downstream monitors whose notifications this alert stands in for"
                        >
                          Affects {pluralize(alert.impacted_count ?? 0, 'monitor')}
                        </span>
                      )}
                      <TriagePill triage={alert.triage} />
                    </div>
                  )}
                </td>
                <td className="hidden whitespace-nowrap tabular-nums md:table-cell">
                  <div className="text-slate-300" title={formatDateTimeFull(alert.triggered_at)}>
                    {formatDateTime(alert.triggered_at)}
                  </div>
                </td>
                <td className="hidden whitespace-nowrap tabular-nums md:table-cell">
                  <ElapsedTime start={alert.triggered_at} end={alert.resolved_at} className="block text-slate-300" />
                  <div
                    className="mt-0.5 text-xs text-slate-500"
                    title={alert.resolved_at ? formatDateTimeFull(alert.resolved_at) : undefined}
                  >
                    {alert.resolved_at ? `ended ${formatDateTime(alert.resolved_at)}` : 'ongoing'}
                  </div>
                  {/* The alerter rewrites failure_count with the failures in
                      its current window on every evaluation, so it is a
                      recent count, not the number that triggered the alert. */}
                  <div className="mt-0.5 text-xs text-slate-500">
                    {pluralize(alert.failure_count, 'recent failure')}
                  </div>
                </td>
                {showActions && (
                  <td className="text-right">
                    {/* One labelled "next step" per row, following the alert
                        lifecycle (active → Acknowledge, acknowledged →
                        Resolve); everything else sits in the ⋮ menu. A wall of
                        identical buttons on every row hides the state, and
                        icon-only verbs make on-call hover to find out. The
                        primary slot has a fixed width so the column lines up;
                        below sm the label collapses to its icon. */}
                    <div className="flex items-center justify-end gap-1.5">
                      {!anyOpen ? null : alert.status === 'active' ? (
                        <Button
                          variant="ghost"
                          size="xs"
                          icon={<UserCheck strokeWidth={1.75} />}
                          onClick={() => handleAcknowledge(alert.id)}
                          disabled={processing?.id === alert.id}
                          loading={processing?.id === alert.id && processing.action === 'ack'}
                          aria-label={`Acknowledge ${alertSubject(alert)}`}
                          aria-keyshortcuts="A"
                          title="Acknowledge (A)"
                          className={PRIMARY_SLOT}
                        >
                          <span className="max-sm:sr-only">Acknowledge</span>
                        </Button>
                      ) : alert.status === 'acknowledged' ? (
                        <Button
                          variant="ghost"
                          size="xs"
                          icon={<CircleCheck strokeWidth={1.75} />}
                          onClick={() => handleResolve(alert.id)}
                          disabled={processing?.id === alert.id}
                          loading={processing?.id === alert.id && processing.action === 'resolve'}
                          aria-label={`Resolve ${alertSubject(alert)}`}
                          aria-keyshortcuts="R"
                          title="Resolve (R)"
                          className={PRIMARY_SLOT}
                        >
                          <span className="max-sm:sr-only">Resolve</span>
                        </Button>
                      ) : (
                        <span className={PRIMARY_SLOT} aria-hidden="true" />
                      )}
                      {alert.status === 'active' || alert.triage?.labelable ? (
                        <RowMenu label={`More actions for ${alertSubject(alert)}`} disabled={processing?.id === alert.id}>
                          {alert.status === 'active' && (
                            <RowMenuItem icon={<CircleCheck strokeWidth={1.75} />} onSelect={() => void handleResolve(alert.id)}>
                              Resolve
                            </RowMenuItem>
                          )}
                          {alert.triage?.labelable && alert.triage.labelled && (
                            <RowMenuItem icon={<Tag strokeWidth={1.75} />} onSelect={() => setLabelling({ alert, mode: 'prompt' })}>
                              {triageMenuCopy(alert.triage).change}
                            </RowMenuItem>
                          )}
                          {alert.triage?.labelable && !alert.triage.labelled && (
                            <>
                              <RowMenuItem icon={<Check strokeWidth={1.75} />} onSelect={() => void handleConfirmTriage(alert)}>
                                {triageMenuCopy(alert.triage).confirm}
                              </RowMenuItem>
                              <RowMenuItem
                                icon={<PencilLine strokeWidth={1.75} />}
                                onSelect={() => setLabelling({ alert, mode: 'correct' })}
                              >
                                {triageMenuCopy(alert.triage).correct}
                              </RowMenuItem>
                            </>
                          )}
                        </RowMenu>
                      ) : (
                        <span className="h-8 w-8 shrink-0" aria-hidden="true" />
                      )}
                    </div>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {/* Shortcut hint, for keyboard users only: it shows while a control in
          the list has keyboard focus (the buttons also carry
          aria-keyshortcuts for screen readers). */}
      {canWrite && anyOpen && (
        <p className="hidden items-center gap-1.5 border-t border-divider px-4 py-2 text-xs text-slate-500 group-has-[:focus-visible]/table:flex">
          On a focused row: <kbd className={KBD}>A</kbd> acknowledge · <kbd className={KBD}>R</kbd> resolve ·{' '}
          <kbd className={KBD}>↑</kbd>
          <kbd className={KBD}>↓</kbd> move
        </p>
      )}
      {labelling && (
        <TriageLabelDialog
          alert={labelling.alert}
          initialMode={labelling.mode}
          onClose={() => setLabelling(null)}
          onLabelled={handleDialogLabelled}
        />
      )}
    </div>
  );
}
