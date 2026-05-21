import type {
  Alert,
  DashboardFailureEvent,
  DashboardOpsSummary,
  DashboardProblemMonitor,
  DashboardStats,
} from './types';

export type OperationalTone = 'critical' | 'attention' | 'stable' | 'clean';

export type OperationalSummary = {
  label: string;
  description: string;
  tone: OperationalTone;
  attentionCount: number;
  primaryActionLabel: string;
  primaryActionHref: string;
  metrics: Array<{
    label: string;
    value: string;
    detail?: string;
    tone?: OperationalTone;
  }>;
};

export type ActivityTimelineItem = {
  id: string;
  label: string;
  title: string;
  detail: string;
  occurredAt: string;
  relativeTime: string;
  tone: 'success' | 'warning' | 'danger' | 'info';
};

type OperationalSummaryInput = {
  opsSummary?: DashboardOpsSummary | null;
  problemMonitorsCount: number;
  recentFailuresCount: number;
  overallUptime: number;
  avgResponseMs: number;
};

export function formatPercent(value: number): string {
  return `${Number.isFinite(value) ? value.toFixed(2) : '0.00'}%`;
}

export function formatRelativeTimeFrom(dateString: string, now = new Date()): string {
  const date = new Date(dateString);
  const seconds = Math.max(0, Math.floor((now.getTime() - date.getTime()) / 1000));
  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

export function buildOperationalSummary(input: OperationalSummaryInput): OperationalSummary {
  const downMonitors = input.opsSummary?.down_monitors ?? 0;
  const activeAlerts = input.opsSummary?.active_alerts ?? 0;
  const pausedMonitors = input.opsSummary?.paused_monitors ?? 0;
  const attentionCount = Math.max(input.problemMonitorsCount, downMonitors + activeAlerts);
  const actionNeeded = downMonitors > 0 || activeAlerts > 0;
  const hasRecentResolvedHistory = input.recentFailuresCount > 0 || input.problemMonitorsCount > 0;

  const label = actionNeeded
    ? 'Action needed'
    : hasRecentResolvedHistory
      ? 'No active outage'
      : 'No active outages';

  const description = actionNeeded
    ? 'Some monitors or alerts need operator review.'
    : hasRecentResolvedHistory
      ? 'Customer-facing checks are healthy. Recent issues have been resolved.'
      : 'Customer-facing checks are healthy.';

  const tone: OperationalTone = actionNeeded
    ? 'critical'
    : attentionCount > 0
      ? 'attention'
      : hasRecentResolvedHistory
        ? 'stable'
        : 'clean';

  return {
    label,
    description,
    tone,
    attentionCount,
    primaryActionLabel: 'View monitors',
    primaryActionHref: '/monitors',
    metrics: [
      {
        label: '30-day uptime',
        value: formatPercent(input.overallUptime),
        detail: 'Selected range',
        tone: input.overallUptime >= 99 ? 'clean' : input.overallUptime >= 95 ? 'attention' : 'critical',
      },
      {
        label: 'Avg response',
        value: `${Math.round(input.avgResponseMs || 0)}ms`,
        detail: 'HTTP checks',
      },
      {
        label: 'Attention',
        value: String(attentionCount),
        detail: pausedMonitors > 0 ? `${pausedMonitors} paused` : 'Monitors',
        tone: attentionCount > 0 ? 'attention' : 'clean',
      },
      {
        label: 'Active alerts',
        value: String(activeAlerts),
        detail: activeAlerts > 0 ? 'Needs review' : 'None',
        tone: activeAlerts > 0 ? 'critical' : 'clean',
      },
    ],
  };
}

function monitorStatusPriority(monitor: DashboardProblemMonitor): number {
  if (monitor.current_status === 'error') return 0;
  if (monitor.current_status === 'failure') return 1;
  if (monitor.uptime < 99) return 2;
  return 3;
}

export function sortNeedsAttention(monitors: DashboardProblemMonitor[]): DashboardProblemMonitor[] {
  return [...monitors].sort((a, b) => {
    const priorityDelta = monitorStatusPriority(a) - monitorStatusPriority(b);
    if (priorityDelta !== 0) return priorityDelta;

    const uptimeDelta = a.uptime - b.uptime;
    if (uptimeDelta !== 0) return uptimeDelta;

    const aLatest = a.latest_failure_at ? new Date(a.latest_failure_at).getTime() : 0;
    const bLatest = b.latest_failure_at ? new Date(b.latest_failure_at).getTime() : 0;
    return bLatest - aLatest;
  });
}

type BuildActivityTimelineInput = {
  failures: DashboardFailureEvent[];
  alerts: Alert[];
  now?: Date;
};

function alertLabel(alert: Alert): string {
  if (alert.status === 'resolved') return 'Alert resolved';
  if (alert.status === 'acknowledged') return 'Alert acknowledged';
  return 'Alert triggered';
}

function alertTone(alert: Alert): ActivityTimelineItem['tone'] {
  if (alert.status === 'resolved') return 'success';
  if (alert.status === 'acknowledged') return 'warning';
  return 'danger';
}

function failureLabel(failure: DashboardFailureEvent): string {
  if (failure.state === 'resolved') return 'Failure resolved';
  if (failure.status === 'error') return 'Monitor issue detected';
  return 'Failure detected';
}

function failureTone(failure: DashboardFailureEvent): ActivityTimelineItem['tone'] {
  if (failure.state === 'resolved') return 'success';
  if (failure.result_source === 'platform') return 'info';
  return failure.status === 'error' ? 'warning' : 'danger';
}

export function buildActivityTimeline(input: BuildActivityTimelineInput): ActivityTimelineItem[] {
  const now = input.now ?? new Date();
  const failureItems: ActivityTimelineItem[] = input.failures.map((failure) => ({
    id: `failure-${failure.check_result_id}`,
    label: failureLabel(failure),
    title: failure.monitor_name,
    detail: [
      failure.result_source === 'platform' ? 'Platform event' : failure.status,
      typeof failure.latency_ms === 'number' ? `${failure.latency_ms}ms` : undefined,
    ].filter(Boolean).join(' · '),
    occurredAt: failure.occurred_at,
    relativeTime: formatRelativeTimeFrom(failure.occurred_at, now),
    tone: failureTone(failure),
  }));

  const alertItems: ActivityTimelineItem[] = input.alerts.map((alert) => ({
    id: `alert-${alert.id}`,
    label: alertLabel(alert),
    title: alert.monitor_name || alert.policy_name || 'Alert',
    detail: [
      alert.policy_name,
      `${alert.failure_count} failure${alert.failure_count === 1 ? '' : 's'}`,
    ].filter(Boolean).join(' · '),
    occurredAt: alert.triggered_at,
    relativeTime: formatRelativeTimeFrom(alert.triggered_at, now),
    tone: alertTone(alert),
  }));

  return [...failureItems, ...alertItems].sort(
    (a, b) => new Date(b.occurredAt).getTime() - new Date(a.occurredAt).getTime(),
  );
}
