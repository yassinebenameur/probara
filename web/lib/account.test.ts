import assert from 'node:assert/strict';
import test from 'node:test';
import { isLocked, sessionClientLabel, sessionMethodLabel, sortSessions } from './account';
import type { AdminSession } from './types';

const now = new Date('2026-11-17T12:00:00Z');

test('isLocked reads only a lock still in force', () => {
  assert.equal(isLocked({}, now), false);
  assert.equal(isLocked({ locked_until: '2026-11-17T12:10:00Z' }, now), true);
  assert.equal(isLocked({ locked_until: '2026-11-17T11:59:59Z' }, now), false);
  assert.equal(isLocked({ locked_until: 'not a date' }, now), false);
});

test('session labels', () => {
  assert.equal(sessionMethodLabel('oidc'), 'SSO');
  assert.equal(sessionMethodLabel('password'), 'Password');
  assert.equal(sessionMethodLabel(undefined), '');
  assert.equal(sessionClientLabel({ client: 'Firefox on macOS' }), 'Firefox on macOS');
  assert.equal(sessionClientLabel({ client: '' }), 'Unknown browser');
});

test('sortSessions puts this device first, then the most recently used', () => {
  const session = (id: string, lastUsed: string, current = false): AdminSession => ({
    id,
    created_at: '2026-11-01T00:00:00Z',
    last_used_at: lastUsed,
    expires_at: '2026-12-01T00:00:00Z',
    current,
  });
  const sorted = sortSessions([
    session('old', '2026-11-10T00:00:00Z'),
    session('here', '2026-11-05T00:00:00Z', true),
    session('new', '2026-11-16T00:00:00Z'),
  ]);
  assert.deepEqual(sorted.map((s) => s.id), ['here', 'new', 'old']);
});
