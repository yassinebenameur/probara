import assert from 'node:assert/strict';
import test from 'node:test';
import { publishNotice, subscriptionAvailability } from './status-page-subscribers';

test('subscriptions are active only when on and the page is not gated', () => {
  assert.equal(subscriptionAvailability({ enable_email_subscriptions: true }, 'public').active, true);
  assert.equal(subscriptionAvailability({ enable_email_subscriptions: true }, 'unlisted').active, true);
  assert.equal(subscriptionAvailability({ enable_email_subscriptions: true }, undefined).active, true);
  assert.equal(subscriptionAvailability({ enable_email_subscriptions: false }, 'public').active, false);
  assert.equal(subscriptionAvailability(undefined, 'public').active, false);
  for (const gated of ['password', 'private'] as const) {
    const a = subscriptionAvailability({ enable_email_subscriptions: true }, gated);
    assert.equal(a.active, false);
    assert.match(a.reason ?? '', /password-protected or private/);
  }
});

test('the publish form says how many subscribers are emailed', () => {
  const on = { enable_email_subscriptions: true };
  assert.equal(publishNotice(on, 'public', 1), '1 email subscriber will be notified when you publish.');
  assert.equal(publishNotice(on, 'public', 42), '42 email subscribers will be notified when you publish.');
  assert.equal(publishNotice(on, 'public', 0), 'No email subscribers yet: nobody is emailed.');
  assert.equal(publishNotice(on, 'public', null), null);
  assert.equal(publishNotice({}, 'public', 5), null);
  assert.equal(publishNotice(on, 'private', 5), null);
});
