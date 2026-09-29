import assert from 'node:assert/strict';
import test from 'node:test';
import {
  dayTone, formatSlaDuration, formatSlaPct, formatTarget, slaInputError, slaReportQueryString, slaTone, strictestTarget,
  DEFAULT_SLA_TARGET, type SlaInput,
} from './sla';

test('formatSlaDuration picks the two most significant units', () => {
  assert.equal(formatSlaDuration(12), '12s');
  assert.equal(formatSlaDuration(2629), '43m 49s');
  assert.equal(formatSlaDuration(7500), '2h 05m');
  assert.equal(formatSlaDuration(90061), '1d 01h 01m');
  assert.equal(formatSlaDuration(-1008), '-16m 48s');
  assert.equal(formatSlaDuration(null), '—');
});

test('percentages and targets keep their meaningful digits', () => {
  assert.equal(formatSlaPct(99.86111), '99.861%');
  assert.equal(formatSlaPct(null), '—');
  assert.equal(formatTarget(99.9), '99.9%');
  assert.equal(formatTarget(99.95), '99.95%');
});

test('tones: breached is bad, a nearly spent budget warns, no data is none', () => {
  assert.equal(slaTone(false, 50), 'bad');
  assert.equal(slaTone(true, 10), 'warn');
  assert.equal(slaTone(true, 80), 'good');
  assert.equal(slaTone(null, null), 'none');
  assert.equal(dayTone(99.9, 99.9), 'good');
  assert.equal(dayTone(99.5, 99.9), 'warn');
  assert.equal(dayTone(95, 99.9), 'bad');
  assert.equal(dayTone(null, 99.9), 'none');
});

test('strictest target wins; no SLA falls back to the default', () => {
  assert.equal(strictestTarget([]), DEFAULT_SLA_TARGET);
  assert.equal(strictestTarget([{ target_pct: 99 }, { target_pct: 99.95 }]), 99.95);
});

test('report query string', () => {
  assert.equal(slaReportQueryString({}), '');
  assert.equal(slaReportQueryString({ period: '2026-09' }, 'pdf'), '?period=2026-09&format=pdf');
  assert.equal(slaReportQueryString({ from: '2026-09-01', to: '2026-09-30' }), '?from=2026-09-01&to=2026-09-30');
});

test('input validation mirrors the API', () => {
  const base: SlaInput = {
    name: 'Checkout', description: '', target_pct: 99.9, aggregation: 'serial', period: 'monthly',
    timezone: 'UTC', degraded_counts_as_down: false, tags: ['payments'], monitor_ids: [],
  };
  assert.equal(slaInputError(base), null);
  assert.match(slaInputError({ ...base, name: ' ' }) ?? '', /Name/);
  assert.match(slaInputError({ ...base, target_pct: 100 }) ?? '', /Target/);
  assert.match(slaInputError({ ...base, tags: [] }) ?? '', /monitor or tag/);
});
