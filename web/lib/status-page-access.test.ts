import assert from 'node:assert/strict';
import test from 'node:test';
import { isGatedVisibility, kioskUrl, pageAccessError, pageAccessPayload, VISIBILITY_OPTIONS } from './status-page-access';

test('lists the four visibilities in order', () => {
  assert.deepEqual(
    VISIBILITY_OPTIONS.map((o) => o.value),
    ['public', 'unlisted', 'password', 'private'],
  );
  assert.equal(isGatedVisibility('public'), false);
  assert.equal(isGatedVisibility('unlisted'), false);
  assert.equal(isGatedVisibility('password'), true);
  assert.equal(isGatedVisibility('private'), true);
});

test('password visibility needs a password, new or stored', () => {
  const draft = { visibility: 'password' as const, password: '', clearPassword: false };
  assert.match(pageAccessError(draft, false) ?? '', /Set a password/);
  assert.equal(pageAccessError(draft, true), undefined);
  assert.match(pageAccessError({ ...draft, clearPassword: true }, true) ?? '', /Set a password/);
  assert.equal(pageAccessError({ ...draft, password: 'correct horse' }, false), undefined);
});

test('password length is checked like the API', () => {
  const draft = { visibility: 'public' as const, password: 'short', clearPassword: false };
  assert.match(pageAccessError(draft, false) ?? '', /at least 8/);
  assert.match(pageAccessError({ ...draft, password: 'é'.repeat(40) }, false) ?? '', /at most 72 bytes/);
  assert.equal(pageAccessError({ ...draft, password: '' }, false), undefined);
});

test('the payload keeps, clears or replaces the password', () => {
  assert.deepEqual(pageAccessPayload({ visibility: 'private', password: '', clearPassword: false }), { visibility: 'private' });
  assert.deepEqual(pageAccessPayload({ visibility: 'public', password: '', clearPassword: true }), {
    visibility: 'public',
    password: '',
  });
  assert.deepEqual(pageAccessPayload({ visibility: 'password', password: 'new secret', clearPassword: false }), {
    visibility: 'password',
    password: 'new secret',
  });
});

test('kiosk URL carries the token and drops edit mode', () => {
  assert.equal(
    kioskUrl('https://status.example.com/public/status/acme?edit=1', 'spk_abc', 'https://app.example.com'),
    'https://status.example.com/public/status/acme?token=spk_abc',
  );
  assert.equal(
    kioskUrl('/public/status/acme', 'spk_a+b', 'http://localhost:3000'),
    'http://localhost:3000/public/status/acme?token=spk_a%2Bb',
  );
});
