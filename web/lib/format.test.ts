import assert from 'node:assert/strict';
import test from 'node:test';
import {
  formatDate,
  formatDateTime,
  formatDateTimeFull,
  formatDuration,
  formatDurationBetween,
  formatLatency,
  formatRelativeTime,
  formatTimestamp,
  formatUptime,
  isRangeId,
  RANGES,
  rangeStart,
  timezoneLabel,
} from './format';

const NOW = new Date(2026, 9, 9, 12, 0, 0); // 9 Oct 2026, 12:00 local

test('formatDateTime is day-month-time, year only outside the current year', () => {
  assert.equal(formatDateTime(new Date(2026, 9, 2, 15, 19, 53), '—', NOW), '2 Oct, 15:19');
  assert.equal(formatDateTime(new Date(2025, 0, 5, 7, 3), '—', NOW), '5 Jan 2025, 07:03');
  assert.equal(formatDateTime(null), '—');
  assert.equal(formatDateTime(undefined, '-'), '-');
  assert.equal(formatDateTime('not a date'), 'not a date');
});

test('formatDate drops the time', () => {
  assert.equal(formatDate(new Date(2026, 6, 2, 23, 59), '—', NOW), '2 Jul');
});

test('formatTimestamp is ISO-like in one zone', () => {
  const d = new Date(2026, 9, 2, 15, 19, 53);
  assert.equal(formatTimestamp(d), '2026-10-02 15:19:53');
  assert.equal(formatTimestamp(d, { seconds: false }), '2026-10-02 15:19');
  assert.equal(formatTimestamp('2026-10-02T09:05:07Z', { tz: 'utc' }), '2026-10-02 09:05:07');
  assert.equal(formatTimestamp(''), '—');
});

test('timezoneLabel names the zone once for a column header', () => {
  assert.equal(timezoneLabel('utc'), 'UTC');
  assert.match(timezoneLabel('local'), /^UTC([+−]\d{1,2}(:\d{2})?)?$/);
  assert.match(formatDateTimeFull(new Date(2026, 9, 2, 15, 19, 53)), /^2026-10-02 15:19:53 UTC/);
});

test('formatRelativeTime: one recency vocabulary', () => {
  const ago = (s: number) => new Date(NOW.getTime() - s * 1000);
  assert.equal(formatRelativeTime(ago(3), NOW), 'just now');
  assert.equal(formatRelativeTime(ago(-20), NOW), 'just now');
  assert.equal(formatRelativeTime(ago(12), NOW), '12s ago');
  assert.equal(formatRelativeTime(ago(185), NOW), '3 min ago');
  assert.equal(formatRelativeTime(ago(4 * 3600 + 5), NOW), '4 hr ago');
  assert.equal(formatRelativeTime(ago(86400), NOW), '1 day ago');
  assert.equal(formatRelativeTime(ago(3 * 86400), NOW), '3 days ago');
  assert.equal(formatRelativeTime(ago(-180), NOW), 'in 3 min');
  assert.equal(formatRelativeTime(new Date(2026, 6, 2, 10, 0), NOW), '2 Jul');
  assert.equal(formatRelativeTime(null, NOW), '—');
});

test('formatDuration keeps the two most significant units', () => {
  assert.equal(formatDuration(45), '45s');
  assert.equal(formatDuration(200), '3m 20s');
  assert.equal(formatDuration(180), '3m');
  assert.equal(formatDuration(3 * 3600 + 12 * 60 + 9), '3h 12m');
  assert.equal(formatDuration(2 * 86400 + 4 * 3600 + 60), '2d 4h');
  assert.equal(formatDuration(-1), '—');
  assert.equal(formatDuration(null), '—');
  assert.equal(formatDurationBetween(new Date(NOW.getTime() - 90_000), undefined, NOW), '1m 30s');
});

test('formatUptime floors and picks precision by band', () => {
  assert.equal(formatUptime(100), '100%');
  assert.equal(formatUptime(99.996), '99.99%');
  assert.equal(formatUptime(99.99), '99.99%');
  assert.equal(formatUptime(99.9), '99.90%');
  assert.equal(formatUptime(99), '99.00%');
  assert.equal(formatUptime(98.96), '98.9%');
  assert.equal(formatUptime(36.3636), '36.3%');
  // Short of 100 never reads as 100.
  assert.equal(formatUptime(99.99999999), '99.99%');
  assert.equal(formatUptime(99.999999999999), '99.99%');
  assert.equal(formatUptime(0), '0.0%');
  assert.equal(formatUptime(null), '—');
  assert.equal(formatUptime(Number.NaN), '—');
});

test('formatLatency switches to seconds at 1000 ms', () => {
  assert.equal(formatLatency(67.4), '67 ms');
  assert.equal(formatLatency(0), '0 ms');
  assert.equal(formatLatency(1234), '1.2 s');
  assert.equal(formatLatency(1000), '1.0 s');
  assert.equal(formatLatency(10000), '10 s');
  assert.equal(formatLatency(null), '—');
  assert.equal(formatLatency(-5), '—');
});

test('RANGES drive every range control', () => {
  assert.equal(RANGES['24h'].long, 'Last 24 hours');
  assert.equal(RANGES.all.ms, null);
  assert.ok(isRangeId('7d'));
  assert.ok(!isRangeId('week'));
  assert.equal(rangeStart('1h', NOW)?.getTime(), NOW.getTime() - 3_600_000);
  assert.equal(rangeStart('all', NOW), null);
});

test('formatLatency picks the unit after rounding', () => {
  assert.equal(formatLatency(999.4), '999 ms');
  assert.equal(formatLatency(999.6), '1.0 s');
  assert.equal(formatLatency(9949), '9.9 s');
  assert.equal(formatLatency(9960), '10 s');
});
