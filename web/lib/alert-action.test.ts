import assert from 'node:assert/strict';
import test from 'node:test';
import {
  alertStatusLabel,
  describeAlertActionState,
  parseAlertActionResponse,
  readActionToken,
} from './alert-action';

const TOKEN = 'AQEh4m9Q2xYk_-0123456789abcdefABCDEF';

test('reads the token from the location hash only when it is token-shaped', () => {
  assert.equal(readActionToken(`#${TOKEN}`), TOKEN);
  assert.equal(readActionToken(TOKEN), TOKEN);
  assert.equal(readActionToken(''), '');
  assert.equal(readActionToken('#'), '');
  assert.equal(readActionToken('#short'), '');
  assert.equal(readActionToken(`#${TOKEN}<script>`), '');
  assert.equal(readActionToken(`#${TOKEN}&x=1`), '');
});

test('reads every verdict the API answers with', () => {
  assert.deepEqual(parseAlertActionResponse(404, { state: 'invalid' }), { state: 'invalid' });
  assert.equal(parseAlertActionResponse(410, { state: 'expired' })?.state, 'expired');
  assert.equal(parseAlertActionResponse(200, { state: 'already' })?.state, 'already');
  assert.equal(parseAlertActionResponse(409, { state: 'resolved' })?.state, 'resolved');
  assert.equal(parseAlertActionResponse(200, { state: 'acknowledged' })?.state, 'acknowledged');
  const pending = parseAlertActionResponse(200, {
    state: 'pending',
    alert: { subject: 'Checkout API', label: 'Alert Triggered', status: 'active', triggered_at: '2026-11-15T09:00:00Z' },
    expires_at: '2026-11-18T09:30:00Z',
  });
  assert.equal(pending?.alert?.subject, 'Checkout API');
  assert.equal(pending?.expires_at, '2026-11-18T09:30:00Z');
});

test('treats server errors and foreign bodies as failures, not verdicts', () => {
  assert.equal(parseAlertActionResponse(500, { state: 'invalid' }), null);
  assert.equal(parseAlertActionResponse(502, null), null);
  assert.equal(parseAlertActionResponse(404, { error: 'not_found' }), null);
  assert.equal(parseAlertActionResponse(200, { state: 'deleted' }), null);
  assert.equal(parseAlertActionResponse(200, 'pending'), null);
});

test('gives every final state its own copy and leaves pending to the button', () => {
  assert.equal(describeAlertActionState('pending'), null);
  const tones = {
    acknowledged: 'success',
    already: 'info',
    resolved: 'info',
    expired: 'warning',
    invalid: 'danger',
  } as const;
  const titles = new Set<string>();
  for (const [state, tone] of Object.entries(tones)) {
    const copy = describeAlertActionState(state as keyof typeof tones);
    assert.equal(copy?.tone, tone, state);
    assert.ok(copy?.title && copy.text, state);
    titles.add(copy!.title);
  }
  assert.equal(titles.size, Object.keys(tones).length);
  assert.match(describeAlertActionState('expired')!.text, /72 hours/);
});

test('labels the alert status', () => {
  assert.equal(alertStatusLabel('active'), 'Active');
  assert.equal(alertStatusLabel('acknowledged'), 'Acknowledged');
  assert.equal(alertStatusLabel('resolved'), 'Resolved');
});
