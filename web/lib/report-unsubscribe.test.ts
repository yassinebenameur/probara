import assert from 'node:assert/strict';
import test from 'node:test';
import { parseReportUnsubscribeResponse, reportUnsubscribePath } from './report-unsubscribe';

test('report unsubscribe path escapes the token', () => {
  assert.equal(reportUnsubscribePath('abc_-123'), '/api/v1/report-unsubscribe/abc_-123');
  assert.equal(reportUnsubscribePath('a/b?c'), '/api/v1/report-unsubscribe/a%2Fb%3Fc');
});

test('only a JSON state is a verdict on the link', () => {
  assert.deepEqual(parseReportUnsubscribeResponse(404, { state: 'invalid' }), { state: 'invalid' });
  assert.equal(parseReportUnsubscribeResponse(200, { state: 'subscribed', sla_name: 'Checkout' })?.sla_name, 'Checkout');
  assert.equal(parseReportUnsubscribeResponse(502, { state: 'invalid' }), null);
  assert.equal(parseReportUnsubscribeResponse(200, '<html>'), null);
  assert.equal(parseReportUnsubscribeResponse(200, { state: 'nope' }), null);
});
