import assert from 'node:assert/strict';
import test from 'node:test';
import { noticeLabel, noticeOutcome, publishNotice, subscriptionAvailability } from './status-page-subscribers';

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

test('notice outcomes say what was lost', () => {
  const base = { id: 'x', kind: 'incident_published', created_at: '', status: 'planned' as const, recipients: 3, sent: 1, waiting: 0, failed: 1, uncertain: 1, skipped: 0 };
  assert.deepEqual(noticeOutcome(base), { text: '1 sent · 1 failed · 1 uncertain of 3', problem: true });
  assert.deepEqual(noticeOutcome({ ...base, sent: 3, failed: 0, uncertain: 0 }), { text: '3 sent of 3', problem: false });
  assert.equal(noticeOutcome({ ...base, status: 'skipped', skip_reason: 'subscriber mail is not configured on the API' }).problem, true);
  assert.equal(noticeOutcome({ ...base, status: 'skipped', skip_reason: 'the page takes no email subscriptions (turned off)' }).problem, false);
  assert.equal(noticeOutcome({ ...base, recipients: 0 }).text, 'No confirmed subscribers to email');
  assert.equal(noticeLabel({ kind: 'maintenance_cancelled' }), 'Maintenance cancelled');
  assert.equal(noticeLabel({ kind: 'incident_published', subject: 'Incident: Outage' }), 'Incident: Outage');
});
