import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildActivityTimeline,
  buildOperationalSummary,
  countDegraded,
  downMonitorsHref,
  needsAttentionTotals,
  parseFailureReason,
  problemMonitorStatus,
  sortNeedsAttention,
} from './dashboard-view-model';
import type { Alert, DashboardFailureEvent, DashboardProblemMonitor } from './types';

const now = new Date('2026-04-24T12:00:00.000Z');

test('buildOperationalSummary builds the headline from the counts and points at the problem', () => {
  const summary = buildOperationalSummary({
    opsSummary: {
      up_monitors: 4,
      down_monitors: 1,
      paused_monitors: 0,
      maintenance_monitors: 0,
      active_alerts: 0,
      acknowledged_alerts: 0,
      suppressed_alerts: 0,
      unrouted_monitors: 0,
    },
    problemMonitorsCount: 2,
    recentFailuresCount: 0,
    overallUptime: 99.7,
    avgResponseMs: 241,
  });

  assert.equal(summary.label, '1 monitor down');
  assert.equal(summary.tone, 'critical');
  assert.equal(summary.attentionCount, 2);
  assert.deepEqual(summary.primaryAction, { label: 'Review 1 down', href: '/monitors?status=down' });
});

test('buildOperationalSummary names down monitors and active alerts, and sends alert-only states to alerts', () => {
  const ops = {
    up_monitors: 6,
    down_monitors: 16,
    paused_monitors: 0,
    maintenance_monitors: 0,
    active_alerts: 14,
    acknowledged_alerts: 2,
    suppressed_alerts: 3,
    unrouted_monitors: 0,
  };
  const both = buildOperationalSummary({
    opsSummary: ops,
    problemMonitorsCount: 5,
    recentFailuresCount: 10,
    overallUptime: 36.36,
    avgResponseMs: 92,
    totalMonitors: 22,
  });
  assert.equal(both.label, '16 monitors down, 14 active alerts');
  assert.equal(both.metrics.find((m) => m.label === 'Active alerts')?.detail, '2 acknowledged, 3 suppressed');
  assert.equal(both.metrics.find((m) => m.label === 'Uptime')?.value, '36.3%');
  assert.equal(both.metrics.find((m) => m.label === 'Uptime')?.tone, 'critical');
  assert.equal(both.metrics.find((m) => m.label === 'Avg HTTP latency')?.value, '92 ms');

  const alertsOnly = buildOperationalSummary({
    opsSummary: { ...ops, down_monitors: 0, active_alerts: 1, acknowledged_alerts: 0, suppressed_alerts: 0 },
    problemMonitorsCount: 0,
    recentFailuresCount: 0,
    overallUptime: 100,
    avgResponseMs: 50,
  });
  assert.equal(alertsOnly.label, '1 active alert');
  assert.deepEqual(alertsOnly.primaryAction, { label: 'Review alerts', href: '/alerts?status=active' });
});

test('buildOperationalSummary says all monitors are up only when every active one is', () => {
  const ops = {
    up_monitors: 21,
    down_monitors: 0,
    paused_monitors: 1,
    maintenance_monitors: 0,
    active_alerts: 0,
    acknowledged_alerts: 0,
    suppressed_alerts: 0,
    unrouted_monitors: 0,
  };
  const clean = buildOperationalSummary({
    opsSummary: ops,
    problemMonitorsCount: 0,
    recentFailuresCount: 0,
    overallUptime: 100,
    avgResponseMs: 50,
    totalMonitors: 22,
  });
  assert.equal(clean.label, 'All 21 monitors up');
  assert.equal(clean.tone, 'clean');
  assert.equal(clean.primaryAction, null);
  const downTile = clean.metrics.find((m) => m.label === 'Down');
  assert.equal(downTile?.detail, 'of 22 monitors');
  // Paused and maintenance counts sit on hover, so the tile line never truncates.
  assert.equal(downTile?.title, '22 monitors: 1 paused');

  const noData = buildOperationalSummary({
    opsSummary: { ...ops, up_monitors: 19 },
    problemMonitorsCount: 0,
    recentFailuresCount: 0,
    overallUptime: 100,
    avgResponseMs: 50,
    totalMonitors: 22,
  });
  assert.equal(noData.label, '19 monitors up');
});

test('buildOperationalSummary counts a down monitor once even when it has an open alert', () => {
  const summary = buildOperationalSummary({
    opsSummary: {
      up_monitors: 8,
      down_monitors: 12,
      paused_monitors: 1,
      maintenance_monitors: 0,
      active_alerts: 10,
      acknowledged_alerts: 0,
      suppressed_alerts: 0,
      unrouted_monitors: 0,
    },
    problemMonitorsCount: 5,
    recentFailuresCount: 0,
    overallUptime: 44.4,
    avgResponseMs: 53,
  });

  assert.equal(summary.attentionCount, 12);
  assert.equal(summary.metrics.find((metric) => metric.label === 'Down')?.value, '12');
  assert.equal(summary.metrics.find((metric) => metric.label === 'Active alerts')?.value, '10');
});

test('buildOperationalSummary avoids contradictory operational copy when only resolved history exists', () => {
  const summary = buildOperationalSummary({
    opsSummary: {
      up_monitors: 4,
      down_monitors: 0,
      paused_monitors: 0,
      maintenance_monitors: 0,
      active_alerts: 0,
      acknowledged_alerts: 0,
      suppressed_alerts: 0,
      unrouted_monitors: 0,
    },
    problemMonitorsCount: 0,
    recentFailuresCount: 3,
    overallUptime: 99.97,
    avgResponseMs: 120,
  });

  assert.equal(summary.label, 'All 4 monitors up');
  assert.equal(summary.tone, 'stable');
  assert.match(summary.description ?? '', /recovered/i);
});

test('buildOperationalSummary renders no-data uptime as a dash, never 0.00% (S-D1)', () => {
  const summary = buildOperationalSummary({
    opsSummary: {
      up_monitors: 0,
      down_monitors: 0,
      paused_monitors: 2,
      maintenance_monitors: 0,
      active_alerts: 0,
      acknowledged_alerts: 0,
      suppressed_alerts: 0,
      unrouted_monitors: 0,
    },
    problemMonitorsCount: 0,
    recentFailuresCount: 0,
    overallUptime: null,
    avgResponseMs: 0,
  });

  const uptimeTile = summary.metrics.find((metric) => metric.label === 'Uptime');
  assert.ok(uptimeTile);
  assert.equal(uptimeTile.value, '—');
  assert.equal(uptimeTile.tone, undefined);
  assert.match(uptimeTile.detail ?? '', /no checks/i);
});

test('sortNeedsAttention orders by current state, then uptime', () => {
  const row = (id: string, state: DashboardProblemMonitor['current_state'], status: string, uptime: number) => ({
    monitor_id: id,
    monitor_name: id,
    current_status: status,
    current_state: state,
    failure_count: 3,
    error_count: 0,
    uptime,
    latest_failure_at: '2026-04-24T10:00:00.000Z',
  });

  assert.deepEqual(
    sortNeedsAttention([
      row('up-low', 'up', 'success', 60),
      // Degraded reads "success" in current_status; the state still ranks it.
      row('degraded', 'degraded', 'success', 66.6),
      row('down-high', 'down', 'failure', 99.5),
      row('down-low', 'down', 'failure', 10),
    ]).map((monitor) => monitor.monitor_id),
    ['down-low', 'down-high', 'degraded', 'up-low'],
  );
});

const problem = (
  id: string,
  state: DashboardProblemMonitor['current_state'],
  failed: number,
): DashboardProblemMonitor => ({
  monitor_id: id,
  monitor_name: id,
  current_status: state === 'down' ? 'failure' : 'success',
  current_state: state,
  failure_count: failed,
  error_count: 0,
  uptime: 66.6,
  latest_failure_at: '2026-04-24T11:59:00.000Z',
});

const failed = (id: string, monitor: string, at: string, resolvedAt?: string): DashboardFailureEvent => ({
  check_result_id: id,
  monitor_id: monitor,
  monitor_name: monitor,
  status: 'failure',
  result_source: 'monitor',
  error_message: 'status_code: got 504',
  occurred_at: at,
  state: resolvedAt ? 'resolved' : 'firing',
  resolved_at: resolvedAt,
});

const alert = (over: Partial<Alert>): Alert => ({
  id: 'a1',
  tenant_id: 't1',
  monitor_id: 'm2',
  status: 'active',
  kind: 'availability',
  triggered_at: '2026-04-24T11:00:00.000Z',
  failure_count: 2,
  created_at: '2026-04-24T11:00:00.000Z',
  updated_at: '2026-04-24T11:00:00.000Z',
  monitor_name: 'Checkout',
  ...over,
});

test('buildActivityTimeline keeps a degraded monitor on one row in its current state, never Recovered', () => {
  // Failing from one location: every failure has a later pass from another.
  const items = buildActivityTimeline({
    failures: [
      failed('r3', 'Tracking', '2026-04-24T11:59:00.000Z', '2026-04-24T11:59:01.000Z'),
      failed('r2', 'Tracking', '2026-04-24T11:58:00.000Z', '2026-04-24T11:58:01.000Z'),
      failed('r1', 'Tracking', '2026-04-24T11:57:00.000Z', '2026-04-24T11:57:01.000Z'),
    ],
    alerts: [],
    monitors: [problem('Tracking', 'degraded', 720)],
    now,
  });

  assert.deepEqual(
    items.map((item) => [item.title, item.event, item.tone, item.detail]),
    [['Tracking', 'Degraded', 'warning', '720 failed checks']],
  );
  assert.equal(items[0].occurredAt, '2026-04-24T11:59:00.000Z');
  assert.equal(items[0].reason, 'status_code: got 504');
  assert.equal(items[0].href, '/monitors/Tracking');
});

test('buildActivityTimeline joins an alert in range to its monitor row and keeps the rest standalone', () => {
  const since = new Date('2026-04-24T11:00:00.000Z');
  const items = buildActivityTimeline({
    failures: [failed('r1', 'm2', '2026-04-24T11:59:00.000Z'), failed('r0', 'Unlisted', '2026-04-24T11:30:00.000Z')],
    alerts: [
      alert({ id: 'a1', monitor_id: 'm2', triggered_at: '2026-04-24T11:30:00.000Z' }),
      alert({
        id: 'a2',
        monitor_id: 'm3',
        monitor_name: 'Search',
        status: 'resolved',
        triggered_at: '2026-04-24T11:10:00.000Z',
        resolved_at: '2026-04-24T11:23:00.000Z',
      }),
      // Its latest event is before the range: not a change in it.
      alert({ id: 'a3', monitor_id: 'm4', monitor_name: 'Old', status: 'acknowledged', acknowledged_at: '2026-04-23T09:00:00.000Z' }),
    ],
    monitors: [problem('m2', 'down', 56)],
    since,
    now,
  });

  assert.deepEqual(
    items.map((item) => [item.title, item.event, item.detail]),
    [
      ['m2', 'Down', 'Alert fired 30 min ago'],
      // Not on the problem list: the row claims only what the checks show.
      ['Unlisted', 'Checks failing', ''],
      ['Search', 'Alert resolved', 'Lasted 13m'],
    ],
  );
});

test('buildActivityTimeline shows a monitor that is up again as Up', () => {
  const items = buildActivityTimeline({
    failures: [failed('r1', 'm1', '2026-04-24T11:00:00.000Z', '2026-04-24T11:01:00.000Z')],
    alerts: [],
    monitors: [problem('m1', 'up', 1)],
    now,
  });
  assert.deepEqual(items.map((item) => [item.event, item.tone, item.detail]), [['Up', 'success', '1 failed check']]);
});

test('downMonitorsHref carries the dashboard tag scope to the monitors list', () => {
  assert.equal(downMonitorsHref([]), '/monitors?status=down');
  assert.equal(downMonitorsHref(['demo', 'team a']), '/monitors?status=down&tag=demo&tag=team+a');
  const summary = buildOperationalSummary({
    opsSummary: {
      up_monitors: 0,
      down_monitors: 1,
      paused_monitors: 0,
      maintenance_monitors: 0,
      active_alerts: 1,
      acknowledged_alerts: 0,
      suppressed_alerts: 0,
      unrouted_monitors: 0,
    },
    problemMonitorsCount: 1,
    recentFailuresCount: 1,
    overallUptime: 0,
    avgResponseMs: 0,
    httpMonitors: 1,
    tags: ['demo'],
  });
  assert.deepEqual(summary.primaryAction, { label: 'Review 1 down', href: '/monitors?status=down&tag=demo' });
  // HTTP checks ran but none succeeded: not "no HTTP checks".
  assert.equal(summary.metrics.find((m) => m.label === 'Avg HTTP latency')?.detail, 'No successful checks in range');
});

test('problemMonitorStatus reads the state machine first and the latest check only as a fallback', () => {
  // The API maps degraded to current_status "success"; the state wins.
  assert.equal(problemMonitorStatus({ current_state: 'degraded', current_status: 'success' }), 'degraded');
  assert.equal(problemMonitorStatus({ current_state: 'suspect', current_status: 'success' }), 'degraded');
  assert.equal(problemMonitorStatus({ current_state: 'down', current_status: 'failure' }), 'down');
  assert.equal(problemMonitorStatus({ current_state: 'up', current_status: 'success' }), 'up');

  assert.equal(problemMonitorStatus({ current_status: 'failure' }), 'down');
  assert.equal(problemMonitorStatus({ current_status: 'error' }), 'down');
  assert.equal(problemMonitorStatus({ current_status: 'success' }), 'up');
  assert.equal(problemMonitorStatus({ current_status: null }), 'unknown');
  assert.equal(problemMonitorStatus(null), 'unknown');
});

test('countDegraded counts degraded states and waits for the list', () => {
  assert.equal(countDegraded(null), null);
  assert.equal(countDegraded([problem('a', 'degraded', 3), problem('b', 'down', 3), problem('c', 'degraded', 1)]), 2);
});

test('needsAttentionTotals counts a complete list itself and falls back for a capped one', () => {
  const list = [problem('a', 'down', 3), problem('b', 'degraded', 3), problem('c', 'up', 1)];
  // ops_summary says 5 down + 3 degraded, but two of the down are groups the
  // list never holds: a list below the limit is complete, so it is the count.
  assert.deepEqual(needsAttentionTotals(list, 50, { total: 8, unhealthy: 8 }), { total: 3, unhealthy: 2 });
  // A list that filled the limit may be cut off; the ops totals stand in.
  assert.deepEqual(needsAttentionTotals(list, 3, { total: 8, unhealthy: 8 }), { total: 8, unhealthy: 8 });
  assert.deepEqual(needsAttentionTotals(null, 50, { total: 0, unhealthy: 0 }), { total: 0, unhealthy: 0 });
});

test('buildOperationalSummary names degraded monitors next to down ones', () => {
  const ops = {
    up_monitors: 147,
    down_monitors: 5,
    paused_monitors: 5,
    maintenance_monitors: 5,
    active_alerts: 2,
    acknowledged_alerts: 1,
    suppressed_alerts: 0,
    unrouted_monitors: 0,
  };
  const summary = buildOperationalSummary({
    opsSummary: ops,
    problemMonitorsCount: 14,
    degradedMonitors: 3,
    recentFailuresCount: 50,
    overallUptime: 96.8,
    avgResponseMs: 144,
    totalMonitors: 162,
  });
  assert.equal(summary.label, '5 monitors down, 3 degraded, 2 active alerts');
  assert.deepEqual(summary.primaryAction, { label: 'Review 8 monitors', href: '/monitors?status=down' });
  const downTile = summary.metrics.find((m) => m.label === 'Down');
  assert.equal(downTile?.detail, 'of 162, 3 degraded');
  assert.equal(downTile?.title, '162 monitors: 5 down, 3 degraded, 5 paused, 5 in maintenance');

  const degradedOnly = buildOperationalSummary({
    opsSummary: { ...ops, down_monitors: 0, active_alerts: 0 },
    problemMonitorsCount: 3,
    degradedMonitors: 3,
    recentFailuresCount: 50,
    overallUptime: 99.1,
    avgResponseMs: 144,
  });
  assert.equal(degradedOnly.label, '3 monitors degraded');
  assert.equal(degradedOnly.tone, 'attention');
  assert.deepEqual(degradedOnly.primaryAction, { label: 'Review 3 degraded', href: '/monitors?status=down' });
});

test('parseFailureReason categorizes known worker prefixes and passes through the rest', () => {
  assert.deepEqual(parseFailureReason('timeout: context deadline exceeded'), {
    category: 'timeout',
    text: 'timeout: context deadline exceeded',
  });
  assert.equal(parseFailureReason('dns: no such host')?.category, 'dns');
  assert.equal(parseFailureReason('connect: connection refused')?.category, 'connect');
  assert.equal(parseFailureReason('status_code: got 404')?.category, 'status');
  assert.equal(parseFailureReason('latency: 3200ms > 1000ms')?.category, 'latency');
  assert.equal(parseFailureReason('tls: missing certificate info')?.category, 'tls');

  // Unknown prefixes keep the raw text but get no pill category.
  assert.deepEqual(parseFailureReason('redis: NOAUTH Authentication required'), {
    category: null,
    text: 'redis: NOAUTH Authentication required',
  });
  assert.equal(parseFailureReason('no colon here')?.category, null);
  assert.equal(parseFailureReason(''), null);
  assert.equal(parseFailureReason('   '), null);
  assert.equal(parseFailureReason(null), null);
  assert.equal(parseFailureReason(undefined), null);
});
