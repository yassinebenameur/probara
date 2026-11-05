import assert from 'node:assert/strict';
import test from 'node:test';
import { codePending, contactAddressError, resendWait, transportReady } from './contacts';

test('contact addresses are checked per kind', () => {
  assert.equal(contactAddressError('email', 'ops@example.com'), null);
  assert.notEqual(contactAddressError('email', 'Ops <ops@example.com>'), null);
  assert.equal(contactAddressError('sms', '+33 6 12 34 56 78'), null);
  assert.notEqual(contactAddressError('sms', '0612345678'), null);
  assert.equal(contactAddressError('sms', ''), 'Enter an address.');
});

test('resend cooldown and pending codes follow the timestamps', () => {
  const now = new Date('2026-11-05T10:00:00Z');
  assert.equal(resendWait({ code_sent_at: '2026-11-05T09:59:30Z' }, now), 30);
  assert.equal(resendWait({ code_sent_at: '2026-11-05T09:58:00Z' }, now), 0);
  assert.equal(resendWait({}, now), 0);
  assert.equal(codePending({ code_expires_at: '2026-11-05T10:10:00Z' }, now), true);
  assert.equal(codePending({ code_expires_at: '2026-11-05T09:10:00Z' }, now), false);
});

test('transports gate verification per kind', () => {
  assert.equal(transportReady('email', { email: true, sms: false }), true);
  assert.equal(transportReady('sms', { email: true, sms: false }), false);
  assert.equal(transportReady('sms', null), false);
});
