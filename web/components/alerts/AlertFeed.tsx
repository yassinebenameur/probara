'use client';

import { CSSProperties, KeyboardEvent, ReactNode, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { Bell, BellOff, Check, ChevronDown, CircleCheck, Network, PencilLine, Tag, UserCheck } from 'lucide-react';
import { Alert } from '@/lib/types';
import { acknowledgeAlert, labelAlertTriage, resolveAlert } from '@/lib/api';
import { labelErrorMessage } from '@/lib/ai-labels';
import { buildTriageLabelRequest, triageMenuCopy, triagePresentation } from '@/lib/ai-triage';
import { formatClock, formatDate, formatDateTime, formatDateTimeFull, pluralize } from '@/lib/format';
import { formatAlertValue, formatSeriesLabel } from '@/lib/metrics';
import { shortFailureReason } from '@/lib/monitor-utils';
import { ALERT_STATUS_LABEL, TIMELINE_DOT, TIMELINE_TONE_LABEL, alertStatusTone } from '@/lib/status-tones';
import { alertSubject } from '@/lib/alert-timeline';
import {
  SHORTCUT_EXEMPT,
  alertTone,
  bucketShift,
  rowShortcut,
  shortcutEffect,
  type FeedGrain,
  type FeedItem,
} from '@/lib/alert-list';
import { flashStyle } from '@/lib/useRowFlash';
import { errorMessage } from '@/lib/errors';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { useOptionalAlertEvents } from '@/components/alerts/AlertStreamProvider';
import type { AlertSelection } from '@/components/alerts/useAlertSelection';
import TriagePill from '@/components/alerts/TriagePill';
import TriageLabelDialog from '@/components/alerts/TriageLabelDialog';
import { DetailList, DetailRow } from '@/components/monitors/MonitorFactsCard';
import Button from '@/components/ui/Button';
import Checkbox from '@/components/ui/Checkbox';
import EmptyState from '@/components/ui/EmptyState';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import { ElapsedTime } from '@/components/ui/RelativeTime';
import RowMenu, { RowMenuItem } from '@/components/ui/RowMenu';
import SelectCheckbox from '@/components/ui/SelectCheckbox';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { useToast } from '@/components/ui/ToastProvider';
import TypeBadge from '@/components/ui/TypeBadge';
import { FOCUS_RING, FOCUS_RING_INSET } from '@/components/ui/focus';

/** What the feed needs to know about an alert's monitor (from the monitors list). */
export interface FeedMonitor {
  type: string;
  config?: unknown;
}

export interface FeedSection {
  key: string;
  /** Left out for a single-alert view. */
  title?: string;
  caption?: string;
  alerts: Alert[];
  /**
   * `alerts` laid out on the range's calendar (lib/alert-list feedItems):
   * time headings, the gaps where nothing happened, and the alerts that
   * started before the range. Left out, the rows are listed as given.
   */
  items?: FeedItem[];
  grain?: FeedGrain;
  /** More rows than shown: a "Show more" control under the list. */
  /** `auto` also loads more when the end of the list scrolls into view. */
  more?: { label: string; onMore: () => void; auto?: boolean };
  /** A line under the list, e.g. that older alerts were not loaded. */
  note?: string;
  empty: string;
  /** The open rows of this section can be selected for bulk triage. */
  selectable?: boolean;
}

interface AlertFeedProps {
  sections: FeedSection[];
  /** Suppressed downstream alerts folded under a row (lib/alert-list openQueue). */
  folded?: ReadonlyMap<string, Alert[]>;
  /** Rows whose folded alerts are shown, and the toggle. */
  unfolded?: ReadonlySet<string>;
  onToggleFold?: (alertId: string) => void;
  monitors?: ReadonlyMap<string, FeedMonitor>;
  /** Each alert's place among its subject's alerts in the range ("3rd in 24h"). */
  occurrence?: ReadonlyMap<string, { nth: number; total: number }>;
  /** "24h": the range, for the repeat hint. */
  rangeShort?: string;
  /** The row whose details are open, and the toggle. */
  expandedId: string | null;
  onExpand: (alertId: string | null) => void;
  onAlertUpdate?: (alert: Alert) => void;
  /**
   * A triage label was saved for this alert. The owner flips the row's
   * `labelled` flag on its current list (`markTriageLabelled`); passing an id
   * rather than a row keeps a live update made meanwhile.
   */
  onTriageLabelled?: (alertId: string) => void;
  /** A fetch is in flight: skeleton on the first load, dimmed rows after. */
  loading?: boolean;
  /** Shown when no section has a row and none has a title (the single-alert view). */
  emptyTitle?: string;
  /** Row selection for bulk triage (useAlertSelection over the open rows on screen). Writers only. */
  selection?: AlertSelection;
  /** useRowFlash().flashClass: rows whose status just changed flash in their new tone. */
  flashClass?: (id: string) => string;
}

// The row's one primary action (Acknowledge or Resolve) sits in a fixed
// slot so it lines up down the list; an icon square below sm.
const PRIMARY_SLOT = 'shrink-0 max-sm:w-8 max-sm:px-0 sm:w-[7.5rem]';
// A row's selection box keeps its slot (nothing shifts when a selection
// starts) but shows only on row hover or keyboard focus, always once a
// selection exists, and always on touch screens, which have no hover.
const REVEAL_ON_ROW_HOVER =
  'opacity-0 transition-opacity duration-fast ease-standard focus-within:opacity-100 group-hover/row:opacity-100 [@media(hover:none)]:opacity-100';
const REVEAL_ON_LIST_HOVER =
  'opacity-0 transition-opacity duration-fast ease-standard focus-within:opacity-100 group-hover/list:opacity-100 [@media(hover:none)]:opacity-100';
// The key cap the sidebar's tooltips use (components/ui/Tooltip.tsx).
const KBD = 'rounded border border-edge px-1 font-mono text-2xs leading-4 text-slate-400';
const CHIP = 'inline-flex max-w-full items-center gap-1 rounded-full px-2 py-0.5 text-2xs font-medium';
// live-ping's halo colour (see the motion note in app/globals.css).
const FIRING_PING = { '--flash': 'rgb(var(--tw-rose-500) / 0.6)' } as CSSProperties;
const SECTION_LABEL = 'text-2xs font-semibold uppercase tracking-wider text-slate-500';

const ORDINAL = new Intl.PluralRules('en', { type: 'ordinal' });
const ORDINAL_SUFFIX: Record<string, string> = { one: 'st', two: 'nd', few: 'rd', other: 'th' };
const ordinal = (n: number) => `${n}${ORDINAL_SUFFIX[ORDINAL.select(n)] ?? 'th'}`;

function KindTag({ title, children }: { title: string; children: ReactNode }) {
  return (
    <span title={title}>
      <Pill size="xs">{children}</Pill>
    </span>
  );
}

/** What the alert measured, for the kinds that carry a value. */
function kindDetail(alert: Alert): string | null {
  if (alert.kind === 'latency_anomaly' && alert.observed_latency_ms != null && alert.baseline_latency_ms != null) {
    return `${Math.round(alert.observed_latency_ms)} ms vs ~${Math.round(alert.baseline_latency_ms)} ms baseline${
      alert.anomaly_score != null ? ` (${alert.anomaly_score.toFixed(1)}σ)` : ''
    }`;
  }
  if (alert.kind === 'host_metric' && alert.metric_value != null) {
    return `${formatAlertValue(alert.metric_name, alert.metric_value)}${
      alert.threshold_value != null ? ` (threshold ${formatAlertValue(alert.metric_name, alert.threshold_value)})` : ''
    }`;
  }
  if (alert.kind === 'tls_expiry' && alert.metric_value != null) {
    return `expires in ${Math.round(alert.metric_value)}d${
      alert.threshold_value != null ? ` (threshold ${Math.round(alert.threshold_value)}d)` : ''
    }`;
  }
  return null;
}

/**
 * Loads more when the end of a list scrolls into view. It re-arms whenever
 * the row count changes (`rows`), so a batch that still leaves the end on
 * screen loads the next, and a load that adds nothing stops there. The
 * button beside it stays for keyboard and screen-reader users.
 */
function AutoMore({ rows, onMore }: { rows: number; onMore: () => void }) {
  const ref = useRef<HTMLSpanElement>(null);
  const latest = useRef(onMore);
  useEffect(() => {
    latest.current = onMore;
  });
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof IntersectionObserver === 'undefined') return;
    const observer = new IntersectionObserver(
      (entries) => entries.some((entry) => entry.isIntersecting) && latest.current(),
      { rootMargin: '0px 0px 240px 0px' }
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [rows]);
  return <span ref={ref} aria-hidden="true" />;
}

function bucketLabel(at: number, grain: FeedGrain, now: Date): string {
  return grain === 'hour' ? `${dayLabel(at, now)} · ${formatClock(at)}` : dayLabel(at, now);
}

function dayLabel(at: number, now: Date): string {
  const day = new Date(at);
  const days = Math.round((new Date(now).setHours(0, 0, 0, 0) - new Date(day).setHours(0, 0, 0, 0)) / 86_400_000);
  return days === 0 ? 'Today' : days === 1 ? 'Yesterday' : formatDate(day);
}

/**
 * The /alerts list: alerts as rows on a time rail, in sections (the open
 * queue, then the range's history under time headings). One status signal
 * per row (the dot on the rail), one labelled next step, details on demand.
 * Which alerts a section holds, their order and what folds under a root
 * cause are lib/alert-list.ts rules; this renders them and owns the row
 * actions.
 */
export default function AlertFeed({
  sections,
  folded,
  unfolded,
  onToggleFold,
  monitors,
  occurrence,
  rangeShort,
  expandedId,
  onExpand,
  onAlertUpdate,
  onTriageLabelled,
  loading,
  emptyTitle = 'No alerts found',
  selection,
  flashClass,
}: AlertFeedProps) {
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
  // move between the rows' subject links, across sections.
  // The guard (exempt targets, modifiers) is lib/alert-list rowShortcut.
  const handleRowKeyDown = (event: KeyboardEvent<HTMLLIElement>, alert: Alert) => {
    const target = event.target as HTMLElement;
    // A folded alert's row sits inside its parent's: a key pressed there
    // belongs to the inner row only.
    if (target.closest('[data-alert-row]') !== event.currentTarget) return;
    const shortcut = rowShortcut(event, target.isContentEditable || target.closest(SHORTCUT_EXEMPT) !== null);
    if (!shortcut) return;

    if (shortcut === 'next' || shortcut === 'previous') {
      const links = Array.from(
        event.currentTarget.closest('[data-alert-feed]')?.querySelectorAll<HTMLElement>('.alert-row-link') ?? []
      );
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
  // this closure's alerts are the render the action started in, so sending
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

  const shown = sections.flatMap((section) => [
    ...section.alerts,
    ...section.alerts.flatMap((alert) => (unfolded?.has(alert.id) ? folded?.get(alert.id) ?? [] : [])),
  ]);

  // The skeleton is for the first load only; a refetch keeps the rows on
  // screen (dimmed) so the page neither jumps nor loses its scroll.
  if (shown.length === 0 && loading) return <TableSkeleton rows={5} columns={3} label="Loading alerts" />;
  if (shown.length === 0 && sections.every((section) => !section.title)) {
    return (
      <EmptyState
        icon={<Bell strokeWidth={1.75} />}
        title={emptyTitle}
        description="Alerts appear here when a monitor goes down."
      />
    );
  }

  const anyOpen = shown.some((alert) => alert.status !== 'resolved');
  const selectable = canWrite && selection !== undefined;
  const selectionPinned = (selection?.selected.size ?? 0) > 0;
  const now = new Date();

  const renderRow = (alert: Alert, section: FeedSection, child: boolean, dated = false) => {
    const subject = alertSubject(alert);
    const tone = alertTone(alert);
    const open = alert.status !== 'resolved';
    const expanded = expandedId === alert.id;
    const busy = processing?.id === alert.id;
    const monitor = alert.monitor_id ? monitors?.get(alert.monitor_id) : undefined;
    const detail = kindDetail(alert);
    const reason = alert.last_error ? shortFailureReason(alert.last_error) || alert.last_error : null;
    const repeat = occurrence?.get(alert.id);
    const kids = folded?.get(alert.id) ?? [];
    const triggered = new Date(alert.triggered_at);
    // Under a time heading the date is already said; elsewhere an alert from
    // another day shows its date, since a bare clock time would mislead.
    const timeText =
      dated || triggered.toDateString() === now.toDateString() ? formatClock(triggered) : formatDate(triggered);
    const hasChips =
      (alert.root_cause_monitor_name && alert.root_cause_monitor_id !== alert.monitor_id) ||
      (alert.suppression_reason === 'dependency' && !alert.root_cause_monitor_name) ||
      (alert.impacted_count ?? 0) > 0 ||
      triagePresentation(alert.triage).visible ||
      (repeat && repeat.nth > 1);
    const flashing = flashClass?.(alert.id) || '';

    return (
      <li
        key={alert.id}
        id={`alert-${alert.id}`}
        data-alert-row
        // Checked rows fill like the monitors list (never orange); the
        // focused row is the one A / R act on, so it shows.
        className={`group/row scroll-mt-24 border-b border-divider last:border-b-0 has-[:focus-visible]:bg-well ${
          selection?.selected.has(alert.id) ? '!bg-well-strong' : expanded ? 'bg-well' : ''
        } ${flashing}`}
        style={flashing ? flashStyle(alertStatusTone(alert.status)) : undefined}
        onKeyDown={(event) => handleRowKeyDown(event, alert)}
      >
        <div className={`flex items-stretch gap-x-2 pr-2 sm:gap-x-3 sm:pr-3 ${child ? 'pl-6 sm:pl-10' : 'pl-2 sm:pl-3'}`}>
          {selectable && selection && (
            <div className="flex w-4 shrink-0 items-center">
              {open && section.selectable ? (
                <SelectCheckbox
                  checked={selection.selected.has(alert.id)}
                  onChange={() => selection.toggle(alert.id)}
                  label={`Select alert for ${subject}`}
                  className={selectionPinned ? 'opacity-100' : REVEAL_ON_ROW_HOVER}
                />
              ) : null}
            </div>
          )}
          <div
            className="flex w-12 shrink-0 items-center justify-end whitespace-nowrap font-mono text-xs tabular-nums text-slate-400"
            title={formatDateTimeFull(alert.triggered_at)}
          >
            {timeText}
          </div>
          {/* The rail: one line down the section, the alert's state as its dot. */}
          <div className="relative w-3 shrink-0" aria-hidden="true">
            <span className="absolute inset-y-0 left-1/2 w-px -translate-x-1/2 bg-divider" />
            {/* An alert nobody has picked up yet pings, so the rows that need
                someone stand out from the acknowledged and suppressed ones. */}
            {tone === 'active' && (
              <span
                className="absolute left-1/2 top-1/2 h-[9px] w-[9px] -translate-x-1/2 -translate-y-1/2 animate-live-ping rounded-full"
                style={FIRING_PING}
              />
            )}
            <span
              className={`absolute left-1/2 top-1/2 h-[9px] w-[9px] -translate-x-1/2 -translate-y-1/2 rounded-full ring-[3px] ring-[rgb(var(--panel-rgb))] ${TIMELINE_DOT[tone]}`}
            />
          </div>
          <div className="min-w-0 flex-1 py-2.5">
            <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
              <span className="sr-only">{tone === 'suppressed' ? 'Suppressed' : ALERT_STATUS_LABEL[alert.status]}:</span>
              {alert.kind === 'mesh_edge' ? (
                <Link
                  href="/locations?tab=mesh"
                  className={`alert-row-link inline-flex min-w-0 items-center gap-1.5 rounded-sm text-sm font-medium text-white transition-colors hover:text-cyan-300 ${FOCUS_RING}`}
                  title="Inter-location mesh path down — view the connectivity matrix"
                >
                  <Network className="h-3.5 w-3.5 shrink-0 text-slate-400" strokeWidth={1.75} />
                  <span className="truncate">{subject}</span>
                </Link>
              ) : (
                <Link
                  href={`/monitors/${alert.monitor_id}`}
                  className={`alert-row-link min-w-0 truncate rounded-sm text-sm font-medium text-white transition-colors hover:text-cyan-300 ${FOCUS_RING}`}
                >
                  {subject}
                </Link>
              )}
              {monitor && <TypeBadge type={monitor.type} config={monitor.config} size="sm" />}
              {alert.kind === 'mesh_edge' && <KindTag title="Directed inter-location connectivity path">mesh</KindTag>}
              {alert.kind === 'latency_anomaly' && <KindTag title="Latency degraded relative to baseline">latency</KindTag>}
              {alert.kind === 'host_metric' && (
                <KindTag title="Host metric breached its configured threshold">{formatSeriesLabel(alert.metric_name)}</KindTag>
              )}
              {alert.kind === 'tls_expiry' && (
                <KindTag title="TLS certificate is inside its expiry warning window — the endpoint itself is still up">
                  certificate
                </KindTag>
              )}
            </div>
            {(detail || reason) && (
              <p className="mt-0.5 truncate text-xs tabular-nums text-slate-400" title={alert.last_error || undefined}>
                {[detail, reason].filter(Boolean).join(' · ')}
              </p>
            )}
            <p className="mt-0.5 flex flex-wrap gap-x-3 text-xs tabular-nums text-slate-400 md:hidden">
              <ElapsedTime start={alert.triggered_at} end={alert.resolved_at} />
              <span>{alert.resolved_at ? `ended ${formatDateTime(alert.resolved_at)}` : TIMELINE_TONE_LABEL[tone].toLowerCase()}</span>
            </p>
            {hasChips && (
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
                      {alert.suppression_reason === 'dependency' ? 'Suppressed, caused by' : 'Likely caused by'}{' '}
                      {alert.root_cause_monitor_name}
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
                {repeat && repeat.nth > 1 && (
                  <span className={`${CHIP} bg-well-hover text-slate-400`} title={`${pluralize(repeat.total, 'alert')} for ${subject} in this range`}>
                    {ordinal(repeat.nth)} in {rangeShort}
                  </span>
                )}
              </div>
            )}
          </div>
          <div className="hidden w-40 shrink-0 flex-col items-end justify-center whitespace-nowrap py-2.5 text-right tabular-nums md:flex">
            <ElapsedTime start={alert.triggered_at} end={alert.resolved_at} className="text-ui text-slate-200" />
            <span className="mt-0.5 text-xs text-slate-500" title={alert.resolved_at ? formatDateTimeFull(alert.resolved_at) : undefined}>
              {alert.resolved_at ? `ended ${formatDateTime(alert.resolved_at)}` : TIMELINE_TONE_LABEL[tone].toLowerCase()}
            </span>
          </div>
          {/* One labelled "next step" per row, following the alert lifecycle
              (active → Acknowledge, acknowledged → Resolve); everything else
              sits in the ⋮ menu. A wall of identical buttons on every row
              hides the state, and icon-only verbs make on-call hover to find
              out. The primary slot has a fixed width so the column lines up;
              below sm the label collapses to its icon. */}
          <div className="flex shrink-0 items-center gap-1.5">
            {canWrite && anyOpen &&
              (alert.status === 'active' ? (
                <Button
                  variant="ghost"
                  size="xs"
                  icon={<UserCheck strokeWidth={1.75} />}
                  onClick={() => handleAcknowledge(alert.id)}
                  disabled={busy}
                  loading={busy && processing?.action === 'ack'}
                  aria-label={`Acknowledge ${subject}`}
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
                  disabled={busy}
                  loading={busy && processing?.action === 'resolve'}
                  aria-label={`Resolve ${subject}`}
                  aria-keyshortcuts="R"
                  title="Resolve (R)"
                  className={PRIMARY_SLOT}
                >
                  <span className="max-sm:sr-only">Resolve</span>
                </Button>
              ) : (
                <span className={PRIMARY_SLOT} aria-hidden="true" />
              ))}
            {canWrite && (alert.status === 'active' || alert.triage?.labelable) ? (
              <RowMenu label={`More actions for ${subject}`} disabled={busy}>
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
                    <RowMenuItem icon={<PencilLine strokeWidth={1.75} />} onSelect={() => setLabelling({ alert, mode: 'correct' })}>
                      {triageMenuCopy(alert.triage).correct}
                    </RowMenuItem>
                  </>
                )}
              </RowMenu>
            ) : (
              canWrite && <span className="h-8 w-8 shrink-0" aria-hidden="true" />
            )}
            <IconButton
              icon={<ChevronDown className={`transition-transform duration-fast ${expanded ? 'rotate-180' : ''}`} strokeWidth={1.75} />}
              label={expanded ? `Hide details for ${subject}` : `Show details for ${subject}`}
              onClick={() => onExpand(expanded ? null : alert.id)}
            />
          </div>
        </div>

        {expanded && (
          <div className="grid animate-fade-in gap-x-8 gap-y-5 border-t border-divider px-4 py-4 sm:grid-cols-2 sm:px-6 xl:grid-cols-3">
            <div>
              <h3 className={`mb-2.5 ${SECTION_LABEL}`}>Lifecycle</h3>
              <DetailList>
                <DetailRow label="Triggered">{formatDateTimeFull(alert.triggered_at)}</DetailRow>
                <DetailRow label="Acknowledged" tone={alert.acknowledged_at ? 'default' : 'note'}>
                  {alert.acknowledged_at ? formatDateTimeFull(alert.acknowledged_at) : 'Not acknowledged'}
                </DetailRow>
                <DetailRow label="Resolved" tone={alert.resolved_at ? 'default' : 'note'}>
                  {alert.resolved_at ? formatDateTimeFull(alert.resolved_at) : 'Ongoing'}
                </DetailRow>
                {/* The alerter rewrites failure_count with the failures in
                    its current window on every evaluation, so it is a
                    recent count, not the number that triggered the alert. */}
                <DetailRow label="Recent failures">{alert.failure_count}</DetailRow>
              </DetailList>
            </div>
            <div className="min-w-0">
              <h3 className={`mb-2.5 ${SECTION_LABEL}`}>Evidence</h3>
              {detail && <p className="mb-1.5 text-sm tabular-nums text-slate-200">{detail}</p>}
              {alert.last_error ? (
                <p className="break-words font-mono text-xs leading-5 text-slate-300">{alert.last_error}</p>
              ) : (
                !detail && <p className="text-sm text-slate-400">No error was recorded.</p>
              )}
            </div>
            {alert.triage?.status === 'ready' && alert.triage.title && (
              <div className="min-w-0">
                <h3 className={`mb-2.5 ${SECTION_LABEL}`}>Suspected cause</h3>
                <p className="text-sm font-medium text-white">{alert.triage.title}</p>
                {alert.triage.summary && <p className="mt-1 text-pretty text-xs leading-5 text-slate-400">{alert.triage.summary}</p>}
              </div>
            )}
          </div>
        )}

        {kids.length > 0 && onToggleFold && (
          <div className="border-t border-divider">
            <button
              type="button"
              onClick={() => onToggleFold(alert.id)}
              aria-expanded={unfolded?.has(alert.id) ?? false}
              className={`flex w-full items-center gap-2 py-1.5 pl-6 pr-3 text-left text-xs text-slate-400 hover:bg-well hover:text-slate-200 sm:pl-10 ${FOCUS_RING_INSET}`}
            >
              <BellOff className="h-3.5 w-3.5 shrink-0" strokeWidth={1.75} aria-hidden="true" />
              {unfolded?.has(alert.id) ? 'Hide' : 'Show'} {pluralize(kids.length, 'downstream alert')} suppressed by this one
              <ChevronDown
                className={`h-3.5 w-3.5 shrink-0 transition-transform duration-fast ${unfolded?.has(alert.id) ? 'rotate-180' : ''}`}
                strokeWidth={1.75}
                aria-hidden="true"
              />
            </button>
            {unfolded?.has(alert.id) && (
              <ul className="border-t border-divider">{kids.map((kid) => renderRow(kid, section, true, false))}</ul>
            )}
          </div>
        )}
      </li>
    );
  };

  const renderSection = (section: FeedSection) => {
    const sectionOpenIds = section.alerts.some((alert) => alert.status !== 'resolved');
    // Whether the rows being rendered sit under a time heading (their date is already said).
    let dated = false;
    return (
      <section key={section.key} aria-label={section.title}>
        {section.title && (
          <div className="mb-2 flex flex-wrap items-center gap-x-3 gap-y-1 px-1">
            {/* The slot stays whenever rows can be selected, so every heading starts at the same x. */}
            {selectable && !(section.selectable && sectionOpenIds) && <span className="block h-4 w-4" aria-hidden="true" />}
            {selectable && selection && section.selectable && sectionOpenIds && (
              <Checkbox
                checked={selection.allSelected}
                indeterminate={selection.someSelected}
                onChange={selection.toggleAll}
                aria-label={selection.allSelected ? 'Deselect all open alerts shown' : 'Select all open alerts shown'}
                className={selectionPinned || selection.allSelected ? 'opacity-100' : REVEAL_ON_LIST_HOVER}
              />
            )}
            <h2 className="text-title font-semibold text-white">{section.title}</h2>
            {section.caption && <span className="text-xs text-slate-500">{section.caption}</span>}
          </div>
        )}
        {section.alerts.length === 0 && !section.items?.length ? (
          <p className="glass px-4 py-6 text-center text-ui text-slate-400">{section.empty}</p>
        ) : (
          <ul className="glass overflow-hidden">
            {(section.items ?? section.alerts.map((alert): FeedItem => ({ kind: 'alert', alert }))).map((item) => {
              if (item.kind === 'alert') return renderRow(item.alert, section, false, dated);
              const grain = section.grain ?? 'day';
              if (item.kind === 'earlier') {
                dated = false;
                return (
                  <li key="earlier" className={`border-b border-divider bg-well px-4 py-1.5 ${SECTION_LABEL}`}>
                    Started before this range
                  </li>
                );
              }
              if (item.kind === 'heading') {
                dated = true;
                return (
                  <li key={`heading:${item.at}`} className={`border-b border-divider bg-well px-4 py-1.5 ${SECTION_LABEL}`}>
                    {bucketLabel(item.at, grain, now)}
                  </li>
                );
              }
              // A stretch with no alert: shown, so the list reads as the calendar it covers.
              const last = grain === 'hour' ? bucketShift(item.to, grain, 1) : item.to;
              return (
                <li
                  key={`gap:${item.from}`}
                  className="flex flex-wrap items-baseline gap-x-3 border-b border-divider px-4 py-2 text-xs text-slate-500 last:border-b-0"
                >
                  <span className={SECTION_LABEL}>
                    {item.from === item.to && grain === 'day'
                      ? bucketLabel(item.from, grain, now)
                      : `${bucketLabel(item.from, grain, now)} – ${
                          // The newest hour is still running: it ends now, not on the next hour.
                          grain === 'hour' && last > now.getTime()
                            ? 'now'
                            : grain === 'hour' && new Date(item.from).toDateString() === new Date(last).toDateString()
                              ? formatClock(last)
                              : bucketLabel(last, grain, now)
                        }`}
                  </span>
                  <span>No alerts</span>
                </li>
              );
            })}
          </ul>
        )}
        {(section.more || section.note) && (
          <div className="mt-3 flex flex-wrap items-center justify-center gap-3 text-xs text-slate-500">
            {section.more && (
              <Button variant="ghost" size="sm" onClick={section.more.onMore}>
                {section.more.label}
              </Button>
            )}
            {section.note && <span>{section.note}</span>}
            {section.more?.auto && <AutoMore rows={section.alerts.length} onMore={section.more.onMore} />}
          </div>
        )}
      </section>
    );
  };

  return (
    // The dim waits 300ms, so a quick refresh never blinks the list.
    <div
      data-alert-feed
      className={`group/table group/list space-y-8 transition-opacity duration-base ${
        loading ? 'pointer-events-none opacity-60 delay-300' : 'delay-0'
      }`}
      aria-busy={loading || undefined}
    >
      <p role="status" className="sr-only">
        {announcement.text && `${announcement.text}${announcement.seq % 2 ? ' ' : ''}`}
      </p>
      {sections.map(renderSection)}
      {/* Shortcut hint, for keyboard users only: it shows while a control in
          the list has keyboard focus (the buttons also carry
          aria-keyshortcuts for screen readers). */}
      {canWrite && anyOpen && (
        <p className="hidden items-center gap-1.5 px-1 text-xs text-slate-500 group-has-[:focus-visible]/table:flex">
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
