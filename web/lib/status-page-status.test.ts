import assert from 'node:assert/strict';
import test from 'node:test';
import {
  componentStatus,
  groupComponentStatus,
  leafComponentStatus,
  leafMemberIds,
  pageHeadline,
} from './status-page-status';
import type { Monitor } from './types';

function mon(id: string, partial: Partial<Monitor> = {}): Monitor {
  return {
    id,
    tenant_id: 't',
    name: id,
    type: 'http',
    interval_seconds: 60,
    timeout_seconds: 10,
    enabled: true,
    created_at: '',
    updated_at: '',
    consecutive_failures_threshold: 1,
    notification_mode: 'default',
    current_state: 'up',
    ...partial,
  } as Monitor;
}

test('leaf status follows the public mapping: suspect stays up, maintenance wins', () => {
  assert.equal(leafComponentStatus({ current_state: 'down' }), 'down');
  assert.equal(leafComponentStatus({ current_state: 'suspect' }), 'up');
  assert.equal(leafComponentStatus({ current_state: 'degraded' }), 'degraded');
  assert.equal(leafComponentStatus({ current_state: 'unknown' }), 'unknown');
  assert.equal(leafComponentStatus({ current_state: undefined }), 'unknown');
  assert.equal(leafComponentStatus({ current_state: 'down', in_maintenance: true }), 'maintenance');
});

test('group status: all up, all down, mixed, nothing reported', () => {
  assert.equal(groupComponentStatus([{ current_state: 'up' }, { current_state: 'suspect' }]), 'up');
  assert.equal(groupComponentStatus([{ current_state: 'down' }, { current_state: 'down' }]), 'down');
  assert.equal(groupComponentStatus([{ current_state: 'up' }, { current_state: 'down' }]), 'degraded');
  assert.equal(groupComponentStatus([{ current_state: 'up' }, { current_state: 'degraded' }]), 'degraded');
  assert.equal(groupComponentStatus([{ current_state: 'unknown' }]), 'unknown');
  assert.equal(groupComponentStatus([]), 'unknown');
  // A member with no state yet does not drag an otherwise healthy group down.
  assert.equal(groupComponentStatus([{ current_state: 'up' }, { current_state: 'unknown' }]), 'up');
});

test('leafMemberIds expands nested groups, dedupes and survives cycles', () => {
  const members = new Map<string, string[]>([
    ['g1', ['a', 'g2', 'b']],
    ['g2', ['b', 'c', 'g1']],
  ]);
  const isGroup = (id: string) => id.startsWith('g');
  assert.deepEqual(leafMemberIds('g1', members, isGroup).sort(), ['a', 'b', 'c']);
  assert.deepEqual(leafMemberIds('g3', members, isGroup), []);
});

test('componentStatus resolves groups through members and is null until they load', () => {
  const group = mon('g', { type: 'group', current_state: 'down' });
  const relay = mon('relay', { current_state: 'down' });
  const ups = mon('ups', { current_state: 'up' });
  const byId = new Map([group, relay, ups].map((m) => [m.id, m]));
  assert.equal(componentStatus(group, byId, new Map()), null);
  // The API's own group state says down; the public page shows the mix.
  assert.equal(componentStatus(group, byId, new Map([['g', ['relay', 'ups']]])), 'degraded');
  assert.equal(componentStatus(relay, byId, new Map()), 'down');
  assert.equal(componentStatus(mon('g2', { type: 'group', in_maintenance: true }), byId, new Map()), 'maintenance');
});

test('pageHeadline matches the public page wording', () => {
  assert.deepEqual(pageHeadline(['up', 'up']), {
    tone: 'ok',
    label: 'All systems operational',
    summary: '2 monitored services are healthy',
    issues: 0,
    total: 2,
  });
  const issues = pageHeadline(['up', 'down', 'degraded', 'maintenance']);
  assert.equal(issues.tone, 'issues');
  assert.equal(issues.label, 'Issues detected');
  assert.equal(issues.summary, '2 of 4 services need attention');
  assert.equal(pageHeadline(['up', 'unknown']).tone, 'issues');
  assert.equal(pageHeadline(['up', 'maintenance']).label, 'Maintenance in progress');
});
