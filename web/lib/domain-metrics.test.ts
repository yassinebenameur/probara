import assert from 'node:assert/strict';
import test from 'node:test';
import { daysUntil, domainFacts, latestDomainMetrics } from './domain-metrics';
import type { CheckResult, DomainMetrics } from './types';

const now = new Date('2026-11-05T12:00:00Z');
const base: DomainMetrics = { domain: 'example.com', registered: true, source: 'rdap', expires_at: '2026-11-20T12:00:00Z', registrar: 'Example Registrar', statuses: ['active'] };

test('latestDomainMetrics skips results without an envelope', () => {
  const results = [
    { id: '1', status: 'error', created_at: '2026-11-05T12:00:00Z' },
    { id: '2', status: 'success', created_at: '2026-11-05T00:00:00Z', metrics_data: { domain: base } },
  ] as CheckResult[];
  assert.equal(latestDomainMetrics(results)?.domain, 'example.com');
  assert.equal(latestDomainMetrics([]), null);
});

test('daysUntil floors like the alerter', () => {
  assert.equal(daysUntil('2026-11-20T04:00:00Z', now), 14);
  assert.equal(daysUntil('2026-11-04T04:00:00Z', now), -2);
  assert.equal(daysUntil('nope', now), null);
});

test('domainFacts: expiry inside the alert window is amber', () => {
  const facts = domainFacts(base, 30, now);
  assert.deepEqual(facts[0], { key: 'expires', label: 'Expires', value: '20 Nov (in 15d)', tone: 'warning' });
  assert.equal(domainFacts(base, 7, now)[0].tone, undefined);
  assert.deepEqual(facts.slice(1).map((f) => f.value), ['Example Registrar', 'active', 'RDAP']);
});

test('domainFacts: expired, unregistered and unpublished expiry', () => {
  assert.equal(domainFacts({ ...base, expires_at: '2026-11-01T12:00:00Z' }, undefined, now)[0].value, 'Expired 1 Nov (4d ago)');
  assert.deepEqual(domainFacts({ ...base, registered: false, source: 'whois' }, 30, now)[0], {
    key: 'registration', label: 'Registration', value: 'Not registered', tone: 'warning',
  });
  const unpublished = domainFacts({ ...base, expires_at: undefined, note: 'the registry publishes no expiration date' }, 30, now);
  assert.equal(unpublished[0].value, 'the registry publishes no expiration date');
});
