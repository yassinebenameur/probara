import assert from 'node:assert/strict';
import test from 'node:test';
import { buildDomainRequest, initialDomainFormState, validateDomainForm } from './monitor-form/domain';
import { buildClonedMonitorInitialData } from './monitor-clone';
import { monitorTarget, monitorTargetLabel } from './monitor-utils';
import { monitorTypeBadge, monitorTypeLabel } from './monitor-type-labels';
import { DEFAULT_ALERTING, STORED_ALERTING, assertWire, cloneSource, storedMonitor } from './monitor-form/testing';

// Domain (registration expiry) monitors: no secrets, a one-hour interval
// floor, twice-a-day default.

test('domain: create from defaults', () => {
  const form = { ...initialDomainFormState(), name: 'example.com', domain: ' example.com ' };
  assert.deepEqual(validateDomainForm(form), {});
  assertWire(buildDomainRequest(form), {
    name: 'example.com',
    type: 'domain',
    config: { domain: 'example.com', min_days_valid: 30, whois_fallback: true },
    interval_seconds: 43200,
    timeout_seconds: 30,
    enabled: true,
    ...DEFAULT_ALERTING,
    tags: [],
    depends_on_ids: [],
    location_ids: [],
    location_quorum: 1,
  });
});

test('domain: create from a filled form', () => {
  const form = {
    ...initialDomainFormState(),
    name: 'Brand',
    domain: 'example.co.uk',
    min_days_valid: 60,
    rdap_server_override: ' https://rdap.nominet.uk/uk/ ',
    whois_fallback: false,
    interval_seconds: 3600,
    tags: 'domains',
  };
  assert.deepEqual(validateDomainForm(form), {});
  assertWire(buildDomainRequest(form).config, {
    domain: 'example.co.uk',
    min_days_valid: 60,
    rdap_server_override: 'https://rdap.nominet.uk/uk/',
    whois_fallback: false,
  });
});

test('domain: edit round trip keeps every field, alerting off stays off', () => {
  const monitor = storedMonitor('domain', { domain: 'example.org', rdap_server_override: 'https://rdap.example/', whois_fallback: false }, {
    interval_seconds: 86400,
    timeout_seconds: 45,
  });
  const form = initialDomainFormState(monitor);
  assert.equal(form.alert_on_expiry, false);
  assertWire(buildDomainRequest(form), {
    name: 'Stored monitor',
    type: 'domain',
    config: { domain: 'example.org', rdap_server_override: 'https://rdap.example/', whois_fallback: false },
    interval_seconds: 86400,
    timeout_seconds: 45,
    enabled: false,
    ...STORED_ALERTING,
    tags: ['prod', 'edge'],
    depends_on_ids: ['dep-1'],
    location_ids: ['loc-a', 'loc-b', 'loc-c'],
    location_quorum: 2,
  });
  // Turning the threshold on adds it; a stored threshold hydrates.
  assertWire(buildDomainRequest({ ...form, alert_on_expiry: true, min_days_valid: 14 }).config, {
    domain: 'example.org',
    min_days_valid: 14,
    rdap_server_override: 'https://rdap.example/',
    whois_fallback: false,
  });
  const withThreshold = initialDomainFormState(storedMonitor('domain', { domain: 'example.org', min_days_valid: 7 }));
  assert.equal(withThreshold.alert_on_expiry, true);
  assert.equal(withThreshold.min_days_valid, 7);
  assert.equal(withThreshold.whois_fallback, true);
});

test('domain: a clone copies the config and schedule, not the alerting', () => {
  const clone = buildClonedMonitorInitialData(
    storedMonitor('domain', { domain: 'example.net', min_days_valid: 21 }, { interval_seconds: 21600, timeout_seconds: 20 })
  );
  assertWire(clone.config, { domain: 'example.net', min_days_valid: 21 });
  const form = initialDomainFormState(undefined, cloneSource('domain', { domain: 'example.net', min_days_valid: 21 }, { interval_seconds: 7200 }));
  const request = buildDomainRequest(form);
  assertWire(
    [request.config, request.interval_seconds, request.timeout_seconds, request.notification_mode],
    [{ domain: 'example.net', min_days_valid: 21, whois_fallback: true }, 7200, 20, 'default']
  );
});

test('domain: validation', () => {
  const base = { ...initialDomainFormState(), name: 'n', domain: 'example.com' };
  assert.ok(validateDomainForm({ ...base, domain: 'https://example.com' }).domain);
  assert.ok(validateDomainForm({ ...base, domain: 'localhost' }).domain);
  assert.ok(validateDomainForm({ ...base, interval_seconds: 600, timeout_seconds: 30 }).interval_seconds);
  assert.ok(validateDomainForm({ ...base, min_days_valid: -1 }).min_days_valid);
  assert.equal(validateDomainForm({ ...base, alert_on_expiry: false, min_days_valid: -1 }).min_days_valid, undefined);
  assert.ok(validateDomainForm({ ...base, rdap_server_override: 'http://rdap.example/' }).rdap_server_override);
});

test('domain: display mirrors', () => {
  assert.equal(monitorTypeLabel('domain'), 'Domain');
  assert.equal(monitorTypeBadge('domain'), 'DOM');
  assert.equal(monitorTargetLabel('domain'), 'domain');
  assert.equal(monitorTarget({ type: 'domain', config: { domain: 'example.com' } }), 'example.com');
});
