import assert from 'node:assert/strict';
import test from 'node:test';
import {
  isGatedVisibility,
  kioskUrl,
  pageAccessError,
  pageAccessPayload,
  visibilityLabel,
  VISIBILITY_OPTIONS,
} from './status-page-access';

test('lists the four visibilities in order and labels the non-public ones', () => {
  assert.deepEqual(
    VISIBILITY_OPTIONS.map((o) => o.value),
    ['public', 'unlisted', 'password', 'private'],
  );
  assert.equal(isGatedVisibility('public'), false);
  assert.equal(isGatedVisibility('unlisted'), false);
  assert.equal(isGatedVisibility('password'), true);
  assert.equal(isGatedVisibility('private'), true);
  assert.equal(isGatedVisibility(undefined), false);
  assert.equal(visibilityLabel('public'), null);
  assert.equal(visibilityLabel(undefined), null);
  assert.equal(visibilityLabel('unlisted'), 'Unlisted');
  assert.equal(visibilityLabel('password'), 'Password');
  assert.equal(visibilityLabel('private'), 'Private');
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
  // Characters, not bytes, for the minimum.
  assert.equal(pageAccessError({ ...draft, password: 'é'.repeat(8) }, false), undefined);
  assert.match(pageAccessError({ ...draft, password: 'é'.repeat(37) }, false) ?? '', /at most 72 bytes/);
  assert.equal(pageAccessError({ ...draft, password: '' }, false), undefined);
});

test('the payload keeps, clears or replaces the password', () => {
  assert.deepEqual(pageAccessPayload({ visibility: 'private', password: '', clearPassword: false }), {
    visibility: 'private',
  });
  assert.deepEqual(pageAccessPayload({ visibility: 'public', password: '', clearPassword: true }), {
    visibility: 'public',
    password: '',
  });
  assert.deepEqual(pageAccessPayload({ visibility: 'password', password: 'new secret', clearPassword: false }), {
    visibility: 'password',
    password: 'new secret',
  });
});

test('kiosk URL carries the token', () => {
  assert.equal(
    kioskUrl('https://status.example.com/public/status/acme', 'spk_abc', 'https://app.example.com'),
    'https://status.example.com/public/status/acme?token=spk_abc',
  );
  assert.equal(
    kioskUrl('/public/status/acme', 'spk_a+b', 'http://localhost:3000'),
    'http://localhost:3000/public/status/acme?token=spk_a%2Bb',
  );
});
