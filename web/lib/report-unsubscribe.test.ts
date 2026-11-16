import assert from 'node:assert/strict';
import test from 'node:test';
import { describeReportUnsubscribeState, parseReportUnsubscribeResponse, readActionToken } from './report-unsubscribe';

test('only a JSON verdict counts; errors and proxies are retried', () => {
  assert.deepEqual(parseReportUnsubscribeResponse(200, { state: 'subscribed', sla_name: 'Checkout' }), { state: 'subscribed', sla_name: 'Checkout' });
  assert.deepEqual(parseReportUnsubscribeResponse(404, { state: 'invalid' }), { state: 'invalid', sla_name: undefined });
  assert.deepEqual(parseReportUnsubscribeResponse(410, { state: 'expired' }), { state: 'expired', sla_name: undefined });
  assert.equal(parseReportUnsubscribeResponse(502, { state: 'invalid' }), null);
  assert.equal(parseReportUnsubscribeResponse(200, '<html>'), null);
  assert.equal(parseReportUnsubscribeResponse(200, { state: 'pending' }), null);
});

test('every final state has copy; subscribed still has a button', () => {
  assert.equal(describeReportUnsubscribeState('subscribed'), null);
  for (const s of ['unsubscribed', 'expired', 'invalid'] as const) {
    assert.ok(describeReportUnsubscribeState(s)?.title);
  }
});

test('the token comes from the fragment only when token-shaped', () => {
  const token = 'A'.repeat(95);
  assert.equal(readActionToken(`#${token}`), token);
  assert.equal(readActionToken('#not a token'), '');
  assert.equal(readActionToken(''), '');
});
