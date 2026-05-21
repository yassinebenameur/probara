import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildActivityTimeline,
  buildOperationalSummary,
  sortNeedsAttention,
} from './dashboard-view-model';

const now = new Date('2026-04-24T12:00:00.000Z');

test('buildOperationalSummary reports action needed for active outage signals', () => {
  const summary = buildOperationalSummary({
    opsSummary: {
      up_monitors: 4,
      down_monitors: 1,
      paused_monitors: 0,
      active_alerts: 0,
      acknowledged_alerts: 0,
    },
    problemMonitorsCount: 2,
    recentFailuresCount: 0,
    overallUptime: 99.7,
    avgResponseMs: 241,
  });

  assert.equal(summary.label, 'Action needed');
  assert.equal(summary.tone, 'critical');
  assert.equal(summary.attentionCount, 2);
  assert.equal(summary.primaryActionLabel, 'View monitors');
});

test('buildOperationalSummary avoids contradictory operational copy when only resolved history exists', () => {
  const summary = buildOperationalSummary({
    opsSummary: {
      up_monitors: 4,
      down_monitors: 0,
      paused_monitors: 0,
      active_alerts: 0,
      acknowledged_alerts: 0,
    },
    problemMonitorsCount: 0,
    recentFailuresCount: 3,
    overallUptime: 99.97,
    avgResponseMs: 120,
  });

  assert.equal(summary.label, 'No active outage');
  assert.equal(summary.tone, 'stable');
  assert.match(summary.description, /resolved/i);
});

test('sortNeedsAttention puts active and lower uptime monitors first', () => {
  const monitors = [
    {
      monitor_id: 'healthy-low',
      monitor_name: 'Healthy but degraded',
      current_status: 'success',
      failure_count: 3,
      error_count: 0,
      uptime: 98.8,
      latest_failure_at: '2026-04-24T10:00:00.000Z',
    },
    {
      monitor_id: 'down',
      monitor_name: 'Down monitor',
      current_status: 'failure',
      failure_count: 1,
      error_count: 0,
      uptime: 99.5,
      latest_failure_at: '2026-04-24T11:00:00.000Z',
    },
    {
      monitor_id: 'error',
      monitor_name: 'Error monitor',
      current_status: 'error',
      failure_count: 0,
      error_count: 2,
      uptime: 97.1,
      latest_failure_at: '2026-04-24T11:30:00.000Z',
    },
  ];

  assert.deepEqual(
    sortNeedsAttention(monitors).map((monitor) => monitor.monitor_id),
    ['error', 'down', 'healthy-low'],
  );
});

test('buildActivityTimeline merges failures and alerts chronologically with clear labels', () => {
  const items = buildActivityTimeline({
    failures: [
      {
        check_result_id: 'r1',
        monitor_id: 'm1',
        monitor_name: 'API',
        status: 'failure',
        result_source: 'monitor',
        occurred_at: '2026-04-24T10:00:00.000Z',
        state: 'resolved',
      },
    ],
    alerts: [
      {
        id: 'a1',
        tenant_id: 't1',
        monitor_id: 'm2',
        alert_policy_id: 'p1',
        status: 'active',
        triggered_at: '2026-04-24T11:00:00.000Z',
        failure_count: 2,
        created_at: '2026-04-24T11:00:00.000Z',
        updated_at: '2026-04-24T11:00:00.000Z',
        monitor_name: 'Checkout',
      },
    ],
    now,
  });

  assert.deepEqual(
    items.map((item) => item.label),
    ['Alert triggered', 'Failure resolved'],
  );
});
