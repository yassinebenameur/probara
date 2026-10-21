import assert from 'node:assert/strict';
import test from 'node:test';
import { bulkOutcome, bulkTargets } from './alert-bulk';
import type { Alert } from './types';

const alert = (id: string, status: Alert['status']) => ({ id, status }) as Alert;

test('bulkTargets sends each action only the alerts it can move', () => {
  const targets = bulkTargets([alert('a', 'active'), alert('b', 'acknowledged'), alert('c', 'resolved')]);
  assert.deepEqual(targets.acknowledge, ['a']);
  assert.deepEqual(targets.resolve, ['a', 'b']);
});

test('bulkOutcome reads a full success', () => {
  assert.deepEqual(bulkOutcome('acknowledge', { items: [alert('a', 'acknowledged')], failed: [] }), {
    message: '1 alert acknowledged',
    tone: 'success',
  });
  assert.equal(
    bulkOutcome('resolve', { items: [alert('a', 'resolved'), alert('b', 'resolved')], failed: [] }).message,
    '2 alerts resolved'
  );
});

test('bulkOutcome explains skipped alerts, most common reason first', () => {
  const partial = bulkOutcome('resolve', {
    items: [alert('a', 'resolved')],
    failed: [
      { id: 'b', reason: 'not_found' },
      { id: 'c', reason: 'already_resolved' },
      { id: 'd', reason: 'already_resolved' },
    ],
  });
  assert.equal(partial.tone, 'warning');
  assert.equal(partial.message, '1 alert resolved · 3 skipped (2 already resolved, 1 not found)');

  const none = bulkOutcome('acknowledge', { items: [], failed: [{ id: 'b', reason: 'already_acknowledged' }] });
  assert.deepEqual(none, { message: 'No alerts acknowledged: 1 already acknowledged', tone: 'warning' });
});
