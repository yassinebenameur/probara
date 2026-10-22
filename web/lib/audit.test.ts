import assert from 'node:assert/strict';
import test from 'node:test';
import { auditActor, auditIp, knownActorLabels } from './audit';
import type { AuditEvent } from './types';

function ev(partial: Partial<AuditEvent>): AuditEvent {
  return {
    id: 1,
    occurred_at: '2026-10-10T09:00:00Z',
    actor_type: 'admin_user',
    actor_label: '',
    action: 'monitor.query',
    resource_type: 'monitor',
    resource_id: '',
    outcome: 'success',
    ip: '',
    user_agent: '',
    ...partial,
  };
}

const ADMIN = '29b9d6ff-acd2-4500-a46a-75c83bd07998';

test('a user event without a stored label borrows one from the same actor', () => {
  const events = [ev({ actor_id: ADMIN }), ev({ actor_id: ADMIN, actor_label: 'admin', action: 'auth.login' })];
  const names = knownActorLabels(events);
  assert.deepEqual(auditActor(events[0], names), { label: 'admin', title: ADMIN, kind: 'user' });
});

test('an unknown user reads as "User <prefix>" with the full id in the title', () => {
  assert.deepEqual(auditActor(ev({ actor_id: ADMIN })), { label: 'User 29b9d6ff', title: ADMIN, kind: 'user' });
  assert.equal(auditActor(ev({})).label, '—');
});

test('keys and anonymous calls keep their kind', () => {
  assert.equal(auditActor(ev({ actor_type: 'api_key', actor_id: 'k1' })).label, 'API key');
  assert.equal(auditActor(ev({ actor_type: 'api_key', actor_id: 'k1' })).kind, 'key');
  assert.equal(auditActor(ev({ actor_type: 'anonymous' })).label, 'Anonymous');
});

test('auditIp drops a source port and keeps bare addresses', () => {
  assert.equal(auditIp('[::1]:55907'), '::1');
  assert.equal(auditIp('[2001:db8::1]'), '2001:db8::1');
  assert.equal(auditIp('10.40.0.5:51234'), '10.40.0.5');
  assert.equal(auditIp('10.40.0.5'), '10.40.0.5');
  assert.equal(auditIp('2001:db8::1'), '2001:db8::1');
  assert.equal(auditIp(''), '');
});
