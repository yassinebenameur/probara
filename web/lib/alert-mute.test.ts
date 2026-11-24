import assert from 'node:assert/strict';
import test from 'node:test';
import { isSnoozed, notificationState, snoozeRequest, toLocalInput } from './alert-mute';
import type { Alert } from './types';

const NOW = Date.parse('2026-11-15T12:00:00Z');
const at = (minutes: number) => new Date(NOW + minutes * 60_000).toISOString();

function alert(over: Partial<Alert> = {}): Alert {
  return {
    id: 'a',
    tenant_id: 't',
    monitor_id: 'm',
    status: 'active',
    triggered_at: at(-60),
    failure_count: 1,
    kind: 'availability',
    created_at: at(-60),
    updated_at: at(-60),
    ...over,
  };
}

test('isSnoozed: an open alert whose snooze has not ended', () => {
  assert.equal(isSnoozed(alert(), NOW), false);
  assert.equal(isSnoozed(alert({ snoozed_until: at(30) }), NOW), true);
  assert.equal(isSnoozed(alert({ snoozed_until: at(-1) }), NOW), false, 'ran out since the row was read');
  assert.equal(isSnoozed(alert({ status: 'resolved', snoozed_until: at(30) }), NOW), false);
  assert.equal(isSnoozed(alert({ snoozed_until: 'garbage' }), NOW), false);
});

test('notificationState: acknowledged stops, snoozed holds, resolved has none', () => {
  assert.equal(notificationState(alert(), NOW), 'notifying');
  assert.equal(notificationState(alert({ snoozed_until: at(30) }), NOW), 'snoozed');
  assert.equal(notificationState(alert({ status: 'acknowledged' }), NOW), 'acknowledged');
  assert.equal(notificationState(alert({ status: 'acknowledged', snoozed_until: at(30) }), NOW), 'acknowledged');
  assert.equal(notificationState(alert({ status: 'resolved' }), NOW), null);
});

test('snoozeRequest: presets send a length, a custom end is bounded to 1 minute .. 7 days', () => {
  const hour = snoozeRequest('1h', '', NOW);
  assert.ok('body' in hour);
  assert.deepEqual(hour.body, { duration_minutes: 60 });
  assert.equal(hour.end, NOW + 3_600_000);
  const day = snoozeRequest('24h', '', NOW);
  assert.ok('body' in day);
  assert.deepEqual(day.body, { duration_minutes: 1440 });

  const custom = snoozeRequest('custom', toLocalInput(NOW + 2 * 3_600_000), NOW);
  assert.ok('body' in custom);
  assert.deepEqual(custom.body, { until: new Date(NOW + 2 * 3_600_000).toISOString() });

  for (const [value, why] of [
    ['', 'empty'],
    [toLocalInput(NOW - 3_600_000), 'in the past'],
    [toLocalInput(NOW + 8 * 24 * 3_600_000), 'beyond seven days'],
  ]) {
    assert.ok('error' in snoozeRequest('custom', value, NOW), why);
  }
});
