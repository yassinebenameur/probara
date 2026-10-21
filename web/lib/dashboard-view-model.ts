import type {
  Alert,
  DashboardFailureEvent,
  DashboardGroup,
  DashboardGroupMember,
  DashboardOpsSummary,
  DashboardProblemMonitor,
} from './types';
import { STATUS_RANK, type MonitorDisplayStatus } from './monitor-utils';
import { formatDurationBetween, formatLatency, formatUptime, pluralize } from './format';
import { formatMonitorListParams, parseMonitorListParams, type MonitorListParams } from './monitor-list';
import { MONITOR_STATUS_LABEL, MONITOR_STATUS_TONE, StatusTone, uptimeTone } from './status-tones';

export type OperationalTone = 'critical' | 'attention' | 'stable' | 'clean';

export type OperationalSummary = {
  /** Built from the counts ("16 monitors down, 14 active alerts"), never a generic verdict. */
  label: string;
  /** Optional second fact; absent when the label already says it all. */
  description?: string;
  tone: OperationalTone;
  /** Monitors the Needs attention panel counts: the same number as the Down tile when any are down. */
  attentionCount: number;
  /** Where the problem is; null when there is nothing to go and look at. */
  primaryAction: { label: string; href: string } | null;
  metrics: Array<{
    label: string;
    value: string;
    detail?: string;
    /** Hover text for facts that do not fit the tile. */
    title?: string;
    tone?: OperationalTone;
    /** Where the number leads; the tile becomes a link. */
    href?: string;
    /** Extra context shown only on very wide screens (3xl and up). */
    wideOnly?: boolean;
  }>;
};

/** The monitors list filtered to down monitors (app/monitors/page.tsx reads ?status). */
export const DOWN_MONITORS_HREF = '/monitors?status=down';

/**
 * The down-monitors link for a dashboard scoped to `tags`: the count on the
 * link is the tag-scoped down_monitors, so the list it opens carries the same
 * scope.
 */
export function downMonitorsHref(tags: readonly string[] = []): string {
  if (tags.length === 0) return DOWN_MONITORS_HREF;
  return monitorsListHref({ status: 'down', tags: [...tags] });
}

/** A /monitors deep link: the list's defaults plus `overrides`, written by formatMonitorListParams. */
export function monitorsListHref(overrides: Partial<MonitorListParams>): string {
  const query = formatMonitorListParams({ ...parseMonitorListParams(''), ...overrides });
  return query ? `/monitors?${query}` : '/monitors';
}
/** Same target as OPEN_ALERTS_HREF in components/layout/FleetHealth.tsx (kept here so lib tests stay React-free). */
export const OPEN_ALERTS_HREF = '/alerts?status=active';

export type ActivityTimelineItem = {
  id: string;
  /** Monitor (or alert subject) name: the bold line. */
  title: string;
  /** The monitor's current state (Down, Degraded, Up) or an alert event (Alert fired…). */
  event: string;
  tone: StatusTone;
  /**
   * Secondary fact ("12 failed checks", "Alert fired"); empty when there is
   * none. With `detailAt` the page appends that time, live ("Alert fired 2 min ago").
   */
  detail: string;
  /** Timestamp the detail ends with, rendered as a live relative time. */
  detailAt?: string;
  /** Raw error text of the latest failed check (or open alert); undefined when there is none. */
  reason?: string;
  /** Drill-down for the row; undefined when the subject has no page. */
  href?: string;
  /** When it happened; the page renders it as a live relative time. */
  occurredAt: string;
};

// ─── Failure reason parsing ─────────────────────────────────────────────────
// Workers prefix HTTP error messages with a category (timeout:/dns:/connect:,
// worker/internal/worker/checker.go) and assertion failures with the assertion
// name (status_code:/latency:/tls:). Unrecognized prefixes get no category —
// the raw message is still displayed, just without a pill.

export type FailureReasonCategory = 'timeout' | 'dns' | 'connect' | 'status' | 'latency' | 'tls';

const REASON_PREFIX_CATEGORIES: Record<string, FailureReasonCategory> = {
  timeout: 'timeout',
  dns: 'dns',
  connect: 'connect',
  status_code: 'status',
  latency: 'latency',
  tls: 'tls',
};

export type FailureReason = {
  category: FailureReasonCategory | null;
  text: string;
};

export function parseFailureReason(message: string | null | undefined): FailureReason | null {
  const text = message?.trim();
  if (!text) return null;
  const colon = text.indexOf(':');
  const prefix = colon > 0 ? text.slice(0, colon).trim().toLowerCase() : '';
  return { category: REASON_PREFIX_CATEGORIES[prefix] ?? null, text };
}

/**
 * Display status of a dashboard monitor row. The persisted state machine
 * (current_state) is the monitor's real state: a multi-location monitor
 * failing below quorum is degraded even when its latest check passed, and
 * the API's current_status maps degraded to "success". current_status (the
 * latest check outcome) is only the fallback for rows without a state, such
 * as service-group members.
 */
export function problemMonitorStatus(
  monitor: { current_state?: string | null; current_status?: string | null } | null | undefined,
): MonitorDisplayStatus {
  switch (monitor?.current_state) {
    case 'down':
      return 'down';
    // suspect = failing, not yet confirmed: the same reading the monitors
    // list gives it (getEffectiveMonitorStatus).
    case 'degraded':
    case 'suspect':
      return 'degraded';
    case 'up':
      return 'up';
    case 'unknown':
      return 'unknown';
  }
  switch (monitor?.current_status) {
    case 'failure':
    case 'error':
      return 'down';
    case 'degraded':
      return 'degraded';
    case 'success':
      return 'up';
    default:
      return 'unknown';
  }
}

/** Monitors the problem list shows as currently degraded (null until it loads). */
export function countDegraded(monitors: DashboardProblemMonitor[] | null | undefined): number | null {
  if (!monitors) return null;
  return monitors.filter((monitor) => problemMonitorStatus(monitor) === 'degraded').length;
}

/**
 * Needs attention's header total and "View all" count. The problem list holds
 * leaf monitors only (the API leaves groups out), while ops_summary's down
 * count includes groups, so a list that came back below the fetch limit is
 * complete and is counted itself; only a capped list falls back to the
 * ops-summary totals, which are then the better lower bound.
 */
export function needsAttentionTotals(
  monitors: DashboardProblemMonitor[] | null | undefined,
  listLimit: number,
  fallback: { total: number; unhealthy: number },
): { total: number; unhealthy: number } {
  if (!monitors || monitors.length >= listLimit) return fallback;
  const unhealthy = monitors.filter((monitor) => {
    const status = problemMonitorStatus(monitor);
    return status === 'down' || status === 'degraded';
  }).length;
  return { total: monitors.length, unhealthy };
}

type OperationalSummaryInput = {
  opsSummary?: DashboardOpsSummary | null;
  problemMonitorsCount: number;
  /**
   * Monitors currently degraded (countDegraded over the problem list); null
   * while that list loads. ops_summary has no degraded count: it counts a
   * degraded monitor as up.
   */
  degradedMonitors?: number | null;
  recentFailuresCount: number;
  /** null = no checks in the window; the tile renders "—", never "0.00%". */
  overallUptime: number | null;
  avgResponseMs: number;
  /** Monitors in scope (stats.total_monitors). */
  totalMonitors?: number;
  /** Monitors with HTTP checks; 0 makes the latency tile "—". */
  httpMonitors?: number;
  rangeLabel?: string;
  /** Dashboard tag scope; the down link carries it to the monitors list. */
  tags?: readonly string[];
};

// Healthy values stay neutral; only warn/bad carry colour.
const UPTIME_OPERATIONAL_TONE = { good: 'clean', warn: 'attention', bad: 'critical', none: undefined } as const;

export function buildOperationalSummary(input: OperationalSummaryInput): OperationalSummary {
  const ops = input.opsSummary;
  const downMonitors = ops?.down_monitors ?? 0;
  const upMonitors = ops?.up_monitors ?? 0;
  const activeAlerts = ops?.active_alerts ?? 0;
  const acknowledgedAlerts = ops?.acknowledged_alerts ?? 0;
  const suppressedAlerts = ops?.suppressed_alerts ?? 0;
  const pausedMonitors = ops?.paused_monitors ?? 0;
  const maintenanceMonitors = ops?.maintenance_monitors ?? 0;
  const unroutedMonitors = ops?.unrouted_monitors ?? 0;
  const totalMonitors = input.totalMonitors ?? upMonitors + downMonitors + pausedMonitors + maintenanceMonitors;
  const activeMonitors = Math.max(0, totalMonitors - pausedMonitors - maintenanceMonitors);
  const degradedMonitors = input.degradedMonitors ?? 0;
  const unhealthyMonitors = downMonitors + degradedMonitors;
  const rangePhrase = (input.rangeLabel ?? 'the selected range').toLowerCase();

  // The problem-monitor list is capped server-side, so its length is a lower
  // bound; the down and degraded counts are the real total whenever larger. A
  // down monitor usually also has an open alert, so the two are never summed.
  const attentionCount = Math.max(input.problemMonitorsCount, unhealthyMonitors);
  const actionNeeded = downMonitors > 0 || activeAlerts > 0;
  const hasRecentResolvedHistory = input.recentFailuresCount > 0 || input.problemMonitorsCount > 0;

  const tone: OperationalTone = actionNeeded
    ? 'critical'
    : attentionCount > 0
      ? 'attention'
      : hasRecentResolvedHistory
        ? 'stable'
        : 'clean';

  let label: string;
  let description: string | undefined;
  let primaryAction: OperationalSummary['primaryAction'] = null;
  if (actionNeeded || degradedMonitors > 0) {
    // "monitors" once, on the first count: "5 monitors down, 3 degraded".
    const monitorFacts = ([
      [downMonitors, 'down'],
      [degradedMonitors, 'degraded'],
    ] as const)
      .filter(([count]) => count > 0)
      .map(([count, state], i) => (i === 0 ? `${pluralize(count, 'monitor')} ${state}` : `${count} ${state}`));
    // "active", not "open": the alerts page's Open view adds acknowledged ones.
    const facts = [...monitorFacts, activeAlerts > 0 ? pluralize(activeAlerts, 'active alert') : null].filter(Boolean);
    label = facts.join(', ');
    if (!actionNeeded) description = 'Some locations fail their checks; no monitor is down.';
    // The monitors list's Down filter includes degraded monitors.
    primaryAction = unhealthyMonitors > 0
      ? {
          label: downMonitors > 0 && degradedMonitors > 0
            ? `Review ${unhealthyMonitors} monitors`
            : `Review ${unhealthyMonitors} ${downMonitors > 0 ? 'down' : 'degraded'}`,
          href: downMonitorsHref(input.tags),
        }
      : { label: 'Review alerts', href: OPEN_ALERTS_HREF };
  } else if (attentionCount > 0) {
    label = `${pluralize(attentionCount, 'monitor')} had failed checks`;
    description = `None is down now; the failures were in the ${rangePhrase}.`;
  } else if (totalMonitors === 0) {
    label = 'No monitors yet';
  } else {
    label = upMonitors >= activeMonitors && activeMonitors > 0
      ? `All ${pluralize(activeMonitors, 'monitor')} up`
      : `${pluralize(upMonitors, 'monitor')} up`;
    description = hasRecentResolvedHistory ? `Earlier failures in the ${rangePhrase} have recovered.` : undefined;
  }

  // The tile has room for two short facts; the full split is on hover.
  const fleetSplit = [
    downMonitors > 0 ? `${downMonitors} down` : null,
    degradedMonitors > 0 ? `${degradedMonitors} degraded` : null,
    pausedMonitors > 0 ? `${pausedMonitors} paused` : null,
    maintenanceMonitors > 0 ? `${maintenanceMonitors} in maintenance` : null,
  ].filter(Boolean);

  const alertSplit = [
    acknowledgedAlerts > 0 ? `${acknowledgedAlerts} acknowledged` : null,
    suppressedAlerts > 0 ? `${suppressedAlerts} suppressed` : null,
  ].filter(Boolean);

  const httpMonitors = input.httpMonitors ?? 1;
  const hasLatency = httpMonitors > 0 && input.avgResponseMs > 0;

  return {
    label,
    description,
    tone,
    attentionCount,
    primaryAction,
    metrics: [
      {
        label: 'Uptime',
        value: formatUptime(input.overallUptime),
        detail: input.overallUptime == null ? 'No checks in range' : input.rangeLabel ?? 'Selected range',
        tone: UPTIME_OPERATIONAL_TONE[uptimeTone(input.overallUptime)],
      },
      {
        label: 'Avg HTTP latency',
        value: hasLatency ? formatLatency(input.avgResponseMs) : '—',
        // The range is named once, under Uptime.
        // Latency averages successful checks only: with HTTP monitors in scope
        // and no average, the checks ran and failed — they did not not run.
        detail: hasLatency ? undefined : httpMonitors === 0 ? 'No HTTP monitors' : 'No successful checks in range',
      },
      {
        label: 'Down',
        value: String(downMonitors),
        detail: degradedMonitors > 0
          ? `of ${totalMonitors}, ${degradedMonitors} degraded`
          : `of ${pluralize(totalMonitors, 'monitor')}`,
        title: fleetSplit.length > 0 ? `${pluralize(totalMonitors, 'monitor')}: ${fleetSplit.join(', ')}` : undefined,
        tone: downMonitors > 0 ? 'critical' : 'clean',
      },
      {
        label: 'Active alerts',
        value: String(activeAlerts),
        detail: alertSplit.join(', ') || (activeAlerts > 0 ? 'None acknowledged' : 'None active'),
        tone: activeAlerts > 0 ? 'critical' : 'clean',
      },
      // Wide screens have room for two facts that otherwise live in a banner
      // (NotificationNudge) and a tooltip (the Down tile's split).
      {
        label: 'Unrouted',
        value: String(unroutedMonitors),
        detail: unroutedMonitors > 0 ? 'Alert no one' : 'Every monitor alerts someone',
        tone: unroutedMonitors > 0 ? 'attention' : undefined,
        href: unroutedMonitors > 0 ? monitorsListHref({ unroutedOnly: true, tags: [...(input.tags ?? [])] }) : undefined,
        wideOnly: true,
      },
      {
        label: 'Paused / maintenance',
        value: String(pausedMonitors + maintenanceMonitors),
        detail: pausedMonitors + maintenanceMonitors > 0
          ? `${pausedMonitors} paused, ${maintenanceMonitors} in maintenance`
          : 'All monitors active',
        wideOnly: true,
      },
    ],
  };
}

/**
 * Needs attention order: current state first (down, degraded, then the
 * monitors that are up again), then lower uptime, then the latest failure.
 */
export function sortNeedsAttention(monitors: DashboardProblemMonitor[]): DashboardProblemMonitor[] {
  return [...monitors].sort((a, b) => {
    const stateDelta = STATUS_RANK[problemMonitorStatus(a)] - STATUS_RANK[problemMonitorStatus(b)];
    if (stateDelta !== 0) return stateDelta;

    const uptimeDelta = a.uptime - b.uptime;
    if (uptimeDelta !== 0) return uptimeDelta;

    const aLatest = a.latest_failure_at ? new Date(a.latest_failure_at).getTime() : 0;
    const bLatest = b.latest_failure_at ? new Date(b.latest_failure_at).getTime() : 0;
    return bLatest - aLatest;
  });
}

type BuildActivityTimelineInput = {
  /** Newest failed checks across the fleet (dashboard recent-failures). */
  failures: DashboardFailureEvent[];
  /** Newest alerts (dashboard recent-alerts; the API does not range-filter them). */
  alerts: Alert[];
  /**
   * The problem-monitor list: each monitor's current state and its exact
   * failed-check count for the range.
   */
  monitors?: DashboardProblemMonitor[];
  /** Start of the dashboard range: an alert whose latest event is older did not change in it. */
  since?: Date | null;
  now?: Date;
};

type AlertEvent = { verb: string; event: string; tone: StatusTone; at: string };

function alertEvent(alert: Alert): AlertEvent {
  if (alert.status === 'resolved') {
    return { verb: 'resolved', event: 'Alert resolved', tone: 'success', at: alert.resolved_at || alert.triggered_at };
  }
  if (alert.status === 'acknowledged') {
    return { verb: 'acknowledged', event: 'Acknowledged', tone: 'warning', at: alert.acknowledged_at || alert.triggered_at };
  }
  return { verb: 'fired', event: 'Alert fired', tone: 'danger', at: alert.triggered_at };
}

function alertTitle(alert: Alert): string {
  if (alert.monitor_name) return alert.monitor_name;
  if (alert.source_location_name && alert.target_location_name) {
    return `${alert.source_location_name} to ${alert.target_location_name}`;
  }
  return alert.policy_name || 'Alert';
}

const time = (value: string) => new Date(value).getTime();

/**
 * One row per monitor, newest first: what the monitor is now and what
 * happened to it in the range.
 *
 * Failed checks are per location, so they are not state changes: a monitor
 * failing from one of three locations alternates failed and passing checks,
 * and reading each next pass as a recovery made it flap Down/Recovered. A
 * monitor's failed checks therefore collapse into one row that wears its
 * current state (problem list: down, degraded, up), with the exact
 * failed-check count for the range. Alert lifecycle events are real
 * transitions; one in range joins its monitor's row, or stands alone for a
 * monitor without failed checks (or a location pair). No API reports state
 * transitions yet, so "Recovered" is never inferred.
 */
export function buildActivityTimeline(input: BuildActivityTimelineInput): ActivityTimelineItem[] {
  const now = input.now ?? new Date();
  const since = input.since ? input.since.getTime() : -Infinity;
  const states = new Map((input.monitors ?? []).map((monitor) => [monitor.monitor_id, monitor]));

  // Newest failed check per monitor (and per source: platform errors are the
  // platform's, not the target's).
  const latestFailure = new Map<string, DashboardFailureEvent>();
  for (const failure of input.failures) {
    const key = `${failure.monitor_id}|${failure.result_source === 'platform' ? 'platform' : 'monitor'}`;
    const seen = latestFailure.get(key);
    if (!seen || time(failure.occurred_at) > time(seen.occurred_at)) latestFailure.set(key, failure);
  }

  // Newest alert event in range per monitor; alerts without a monitor stand alone.
  const monitorAlerts = new Map<string, { alert: Alert; event: AlertEvent }>();
  const standaloneAlerts: Array<{ alert: Alert; event: AlertEvent }> = [];
  for (const alert of input.alerts) {
    const event = alertEvent(alert);
    if (time(event.at) < since) continue;
    if (!alert.monitor_id) {
      standaloneAlerts.push({ alert, event });
      continue;
    }
    const seen = monitorAlerts.get(alert.monitor_id);
    if (!seen || time(event.at) > time(seen.event.at)) monitorAlerts.set(alert.monitor_id, { alert, event });
  }

  const items: ActivityTimelineItem[] = [];

  for (const failure of latestFailure.values()) {
    const href = `/monitors/${failure.monitor_id}`;
    if (failure.result_source === 'platform') {
      items.push({
        id: `platform-${failure.monitor_id}`,
        title: failure.monitor_name,
        event: 'Platform',
        tone: 'info',
        detail: 'Check could not run',
        reason: failure.error_message || undefined,
        href,
        occurredAt: failure.occurred_at,
      });
      continue;
    }
    const known = states.get(failure.monitor_id);
    const status = known ? problemMonitorStatus(known) : null;
    const alertInRange = monitorAlerts.get(failure.monitor_id);
    monitorAlerts.delete(failure.monitor_id);
    const failedCount = known ? known.failure_count + known.error_count : 0;
    const occurredAt =
      alertInRange && time(alertInRange.event.at) > time(failure.occurred_at) ? alertInRange.event.at : failure.occurred_at;
    items.push({
      id: `monitor-${failure.monitor_id}`,
      title: failure.monitor_name,
      // Without a known state the row claims only what the checks show.
      event: status ? MONITOR_STATUS_LABEL[status] : 'Checks failing',
      tone: status ? MONITOR_STATUS_TONE[status] : 'danger',
      detail: alertInRange
        ? `Alert ${alertInRange.event.verb}`
        : failedCount > 0
          ? pluralize(failedCount, 'failed check')
          : '',
      detailAt: alertInRange?.event.at,
      reason: failure.error_message || undefined,
      href,
      occurredAt,
    });
  }

  for (const { alert, event } of [...monitorAlerts.values(), ...standaloneAlerts]) {
    items.push({
      id: `alert-${alert.id}`,
      title: alertTitle(alert),
      event: event.event,
      tone: event.tone,
      detail:
        alert.status === 'resolved' && alert.resolved_at
          ? `Lasted ${formatDurationBetween(alert.triggered_at, alert.resolved_at, now)}`
          : '',
      reason: alert.status === 'resolved' ? undefined : alert.last_error || undefined,
      href: alert.monitor_id ? `/monitors/${alert.monitor_id}` : undefined,
      occurredAt: event.at,
    });
  }

  return items.sort((a, b) => time(b.occurredAt) - time(a.occurredAt));
}

// ─── Service groups ──────────────────────────────────────────────────────────

/** Problem-list rows by monitor id: they carry the real state (current_state). */
export type MonitorStates = ReadonlyMap<string, DashboardProblemMonitor>;

type GroupMemberLike = Pick<DashboardGroupMember, 'monitor_id' | 'current_status'>;

/**
 * A group member's display status now. A member's current_status is its
 * latest check in the range; a member on the problem list has its real state
 * (current_state), which wins.
 */
export function groupMemberStatus(member: GroupMemberLike | null | undefined, states?: MonitorStates): MonitorDisplayStatus {
  return problemMonitorStatus(member && (states?.get(member.monitor_id) ?? member));
}

export type GroupHealth = 'down' | 'degraded' | 'up' | 'unknown' | 'empty';

/**
 * A service group's health NOW: its worst member's current state (down,
 * degraded, otherwise up), "unknown" when nothing has data and "empty" for a
 * group without monitors. Failed checks earlier in the range never make a
 * group amber by themselves — on 30d nearly every group has some, and its
 * uptime figure (coloured by uptimeTone) already carries them.
 *
 * Read from the members, never from attention_count: the API returns the
 * worst members first, and their state is what the tile should say.
 */
export function groupHealth(group: DashboardGroup, states?: MonitorStates): GroupHealth {
  if (group.monitor_count === 0) return 'empty';
  const statuses = groupMemberStatuses(group, states);
  if (statuses.includes('down')) return 'down';
  if (statuses.includes('degraded')) return 'degraded';
  if (group.uptime == null && statuses.every((status) => status === 'unknown')) return 'unknown';
  return 'up';
}

function groupMemberStatuses(group: DashboardGroup, states?: MonitorStates): MonitorDisplayStatus[] {
  const members = group.worst_member ? [group.worst_member, ...group.members] : group.members;
  const seen = new Set<string>();
  const out: MonitorDisplayStatus[] = [];
  for (const member of members) {
    if (seen.has(member.monitor_id)) continue;
    seen.add(member.monitor_id);
    out.push(groupMemberStatus(member, states));
  }
  return out;
}

/** Members down or degraded now, from the member preview (the API's worst-first slice). */
export function groupAttentionCount(group: DashboardGroup, states?: MonitorStates): number {
  return groupMemberStatuses(group, states).filter((status) => status === 'down' || status === 'degraded').length;
}

const GROUP_HEALTH_RANK: Record<GroupHealth, number> = { down: 0, degraded: 1, up: 2, unknown: 3, empty: 4 };

/**
 * Worst first: current health (down, degraded, up, no data), then lower
 * uptime, then name. "Ungrouped" (tag null) is not a service and goes last.
 */
export function sortServiceGroups(groups: readonly DashboardGroup[], states?: MonitorStates): DashboardGroup[] {
  const health = new Map(groups.map((group) => [group, groupHealth(group, states)] as const));
  return [...groups].sort((a, b) => {
    if ((a.tag == null) !== (b.tag == null)) return a.tag == null ? 1 : -1;
    const rank = GROUP_HEALTH_RANK[health.get(a)!] - GROUP_HEALTH_RANK[health.get(b)!];
    if (rank !== 0) return rank;
    const au = a.uptime ?? Infinity;
    const bu = b.uptime ?? Infinity;
    if (au !== bu) return au - bu;
    return (a.tag ?? '').localeCompare(b.tag ?? '', undefined, { sensitivity: 'base', numeric: true });
  });
}
