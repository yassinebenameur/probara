import assert from 'node:assert/strict';
import test from 'node:test';
import { alertActionPath, describeAlertActionState, parseAlertActionResponse } from './alert-action';

test('builds the API path with the token escaped', () => {
  assert.equal(alertActionPath('abc_-123'), '/api/v1/alert-actions/abc_-123');
  assert.equal(alertActionPath('a/b?c'), '/api/v1/alert-actions/a%2Fb%3Fc');
});

test('reads every refusal state the API answers with', () => {
  assert.deepEqual(parseAlertActionResponse(404, { state: 'invalid' }), { state: 'invalid' });
  assert.equal(parseAlertActionResponse(410, { state: 'expired' })?.state, 'expired');
  assert.equal(parseAlertActionResponse(409, { state: 'already' })?.state, 'already');
  assert.equal(parseAlertActionResponse(409, { state: 'resolved' })?.state, 'resolved');
  assert.equal(
    parseAlertActionResponse(200, { state: 'pending', alert: { subject: 'API', kind: 'availability', status: 'active', triggered_at: 'x' } })
      ?.alert?.subject,
    'API'
  );
});

test('treats server errors and foreign bodies as failures, not verdicts', () => {
  assert.equal(parseAlertActionResponse(500, { state: 'invalid' }), null);
  assert.equal(parseAlertActionResponse(502, null), null);
  assert.equal(parseAlertActionResponse(404, { error: 'not found' }), null);
  assert.equal(parseAlertActionResponse(200, { state: 'deleted' }), null);
  assert.equal(parseAlertActionResponse(200, 'pending'), null);
});

test('describes each final state and leaves pending to the button', () => {
  assert.equal(describeAlertActionState('pending'), null);
  assert.equal(describeAlertActionState('acknowledged')?.tone, 'success');
  assert.equal(describeAlertActionState('already')?.tone, 'info');
  assert.equal(describeAlertActionState('resolved')?.tone, 'info');
  assert.equal(describeAlertActionState('expired')?.tone, 'warning');
  assert.equal(describeAlertActionState('invalid')?.tone, 'danger');
});
