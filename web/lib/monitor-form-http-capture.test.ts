import assert from 'node:assert/strict';
import test from 'node:test';
import { buildMonitorFormRequest, defaultTimeoutFor, initialMonitorFormState, validateMonitorForm } from './monitor-form/inline';
import { storedMonitor } from './monitor-form/testing';
import type { HTTPMonitorConfig } from './types';

// HTTP failure-response capture: the fields are sent only when capture is on
// (the budget only when not the default), so monitors that never capture
// keep their exact payloads.

const form = (patch: Partial<ReturnType<typeof initialMonitorFormState>> = {}) => ({
  ...initialMonitorFormState(),
  timeout_seconds: defaultTimeoutFor('http'),
  name: 'API',
  url: 'https://api.example.com/health',
  ...patch,
});

const config = (f: ReturnType<typeof form>) => buildMonitorFormRequest('http', f, false).config as HTTPMonitorConfig;

test('capture: off by default and absent from the payload', () => {
  const c = config(form());
  assert.equal('capture_failure_response' in c, false);
  assert.equal('capture_body_bytes' in c, false);
});

test('capture: on sends the flag, the budget only when changed', () => {
  const on = config(form({ capture_failure_response: true }));
  assert.equal(on.capture_failure_response, true);
  assert.equal('capture_body_bytes' in on, false);
  const sized = config(form({ capture_failure_response: true, capture_body_bytes: 8192 }));
  assert.equal(sized.capture_body_bytes, 8192);
  // Turning it off drops both, even with a budget left in the form.
  const off = config(form({ capture_failure_response: false, capture_body_bytes: 8192 }));
  assert.equal('capture_failure_response' in off || 'capture_body_bytes' in off, false);
});

test('capture: an edit round trip keeps the stored settings', () => {
  const monitor = storedMonitor('http', {
    url: 'https://api.example.com',
    method: 'GET',
    capture_failure_response: true,
    capture_body_bytes: 4096,
  });
  const state = initialMonitorFormState(monitor);
  assert.equal(state.capture_failure_response, true);
  assert.equal(state.capture_body_bytes, 4096);
  const c = buildMonitorFormRequest('http', state, false).config as HTTPMonitorConfig;
  assert.equal(c.capture_failure_response, true);
  assert.equal(c.capture_body_bytes, 4096);
});

test('capture: the budget is validated only while capture is on', () => {
  assert.ok(validateMonitorForm('http', form({ capture_failure_response: true, capture_body_bytes: 9000 }), false).capture_body_bytes);
  assert.ok(validateMonitorForm('http', form({ capture_failure_response: true, capture_body_bytes: 0 }), false).capture_body_bytes);
  assert.equal(validateMonitorForm('http', form({ capture_failure_response: false, capture_body_bytes: 0 }), false).capture_body_bytes, undefined);
});
