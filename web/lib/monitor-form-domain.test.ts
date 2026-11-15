import assert from 'node:assert/strict';
import test from 'node:test';
import {
  buildDomainRequest,
  initialDomainFormState,
  validateDomainForm,
} from './monitor-form/domain';
import { domainExpiryState, domainFacts } from './domain-expiry';
import { buildClonedMonitorInitialData } from './monitor-clone';
import { monitorTarget, monitorTargetLabel } from './monitor-utils';
import { CLONED_SCHEDULE, CLONED_TAIL, STORED_ALERTING, assertWire, cloneSource, storedMonitor } from './monitor-form/testing';
import type { DomainInfo } from './types';

// DomainForm's payloads. A domain monitor has no secrets; its defaults are a
// daily schedule, a 30 s timeout, a 30-day window and down at the first
// failed check.

const DOMAIN_DEFAULT_ALERTING = {
  consecutive_failures_threshold: 1,
  notification_mode: 'default',
  dependency_suppression: 'inherit',
  notification_channels: [],
} as const;

test('domain: create from defaults', () => {
  const form = { ...initialDomainFormState(), name: 'example.com registration', domain: ' example.com ' };
  assert.deepEqual(validateDomainForm(form), {});
  assertWire(buildDomainRequest(form), {
    name: 'example.com registration',
    type: 'domain',
    config: { domain: 'example.com', min_days_valid: 30 },
    interval_seconds: 86400,
    timeout_seconds: 30,
    enabled: true,
    ...DOMAIN_DEFAULT_ALERTING,
    tags: [],
    depends_on_ids: [],
    location_ids: [],
    location_quorum: 1,
  });
});

test('domain: create from a filled form', () => {
  const form = {
    ...initialDomainFormState(),
    name: 'Shop domain',
    domain: 'www.shop.example.co.uk',
    min_days_valid: 14,
    interval_seconds: 3600,
    timeout_seconds: 20,
    tags: 'domains, billing',
    location_ids: ['loc-a'],
  };
  assert.deepEqual(validateDomainForm(form), {});
  assertWire(buildDomainRequest(form), {
    name: 'Shop domain',
    type: 'domain',
    config: { domain: 'www.shop.example.co.uk', min_days_valid: 14 },
    interval_seconds: 3600,
    timeout_seconds: 20,
    enabled: true,
    ...DOMAIN_DEFAULT_ALERTING,
    tags: ['domains', 'billing'],
    depends_on_ids: [],
    location_ids: ['loc-a'],
    location_quorum: 1,
  });
});

test('domain: edit round trip keeps every field the form hydrates', () => {
  const monitor = storedMonitor('domain', { domain: 'example.org', min_days_valid: 0 }, { interval_seconds: 43200, timeout_seconds: 45 });
  assertWire(buildDomainRequest(initialDomainFormState(monitor)), {
    name: 'Stored monitor',
    type: 'domain',
    config: { domain: 'example.org', min_days_valid: 0 },
    interval_seconds: 43200,
    timeout_seconds: 45,
    enabled: false,
    ...STORED_ALERTING,
    tags: ['prod', 'edge'],
    depends_on_ids: ['dep-1'],
    location_ids: ['loc-a', 'loc-b', 'loc-c'],
    location_quorum: 2,
  });
  // A stored monitor without min_days_valid shows the API's default.
  assert.equal(initialDomainFormState(storedMonitor('domain', { domain: 'example.org' })).min_days_valid, 30);
});

test('domain: a clone keeps the config and the schedule, not the alerting', () => {
  const initialData = cloneSource('domain', { domain: 'example.net', min_days_valid: 21 }, { interval_seconds: 7200 });
  assertWire(buildDomainRequest(initialDomainFormState(undefined, initialData)), {
    name: 'Source (copy)',
    type: 'domain',
    config: { domain: 'example.net', min_days_valid: 21 },
    ...CLONED_SCHEDULE,
    interval_seconds: 7200,
    enabled: false,
    ...DOMAIN_DEFAULT_ALERTING,
    ...CLONED_TAIL,
  });
  const source = storedMonitor('domain', { domain: 'example.net', min_days_valid: 21 });
  assertWire(buildClonedMonitorInitialData(source).config, { domain: 'example.net', min_days_valid: 21 });
});

test('domain: client-side checks mirror the API', () => {
  const base = { ...initialDomainFormState(), name: 'n', domain: 'example.com' };
  const errors = (overrides: Partial<typeof base>) => validateDomainForm({ ...base, ...overrides });
  assert.ok(errors({ domain: '' }).domain);
  assert.ok(errors({ domain: 'https://example.com' }).domain);
  assert.ok(errors({ domain: 'example.com:443' }).domain);
  assert.ok(errors({ domain: '192.0.2.1' }).domain);
  assert.ok(errors({ domain: '2001:db8::1' }).domain);
  assert.ok(errors({ domain: 'com' }).domain);
  assert.equal(errors({ domain: 'bücher.de' }).domain, undefined);
  assert.ok(errors({ min_days_valid: -1 }).min_days_valid);
  assert.ok(errors({ min_days_valid: 3651 }).min_days_valid);
  assert.ok(errors({ min_days_valid: Number.NaN }).min_days_valid);
  // The one-hour floor is enforced before the API sees it.
  assert.ok(errors({ interval_seconds: 1800, timeout_seconds: 30 }).interval_seconds);
  assert.equal(errors({ interval_seconds: 3600, timeout_seconds: 30 }).interval_seconds, undefined);
  assert.ok(errors({ interval_seconds: 3600, timeout_seconds: 3600 }).timeout_seconds);
});

test('domain: list target is the watched name', () => {
  assert.equal(monitorTarget({ type: 'domain', config: { domain: 'example.com' } }), 'example.com');
  assert.equal(monitorTargetLabel('domain'), 'domain');
});

test('domain: snapshot headline and tone', () => {
  const now = new Date('2026-11-15T12:00:00Z');
  const info = (extra: Partial<DomainInfo>): DomainInfo => ({ domain: 'example.com', registered: true, source: 'rdap', ...extra });
  assert.deepEqual(domainExpiryState(info({ expires_at: '2027-08-13T04:00:00Z' }), 30, now), { headline: 'expires in 270 days', tone: 'good' });
  assert.deepEqual(domainExpiryState(info({ expires_at: '2026-11-27T12:00:00Z' }), 30, now), { headline: 'expires in 12 days', tone: 'warn' });
  assert.deepEqual(domainExpiryState(info({ expires_at: '2026-11-12T12:00:00Z' }), 30, now), { headline: 'expired 3 days ago', tone: 'bad' });
  assert.deepEqual(domainExpiryState(info({ registered: false }), 30, now), { headline: 'not registered', tone: 'bad' });
  assert.deepEqual(domainExpiryState(info({ unknown_reason: 'the registry publishes no expiration date' }), 30, now), {
    headline: 'expiry unknown',
    tone: 'warn',
    reason: 'the registry publishes no expiration date',
  });
  const facts = domainFacts(info({ expires_at: '2027-08-13T04:00:00Z', registrar: 'Example Registrar', server: 'https://rdap.example' }));
  assert.deepEqual(
    facts.map((f) => f.label),
    ['Expires', 'Registrar', 'Source']
  );
  assert.equal(facts[2].value, 'RDAP · https://rdap.example');
});
