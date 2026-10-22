import assert from 'node:assert/strict';
import test from 'node:test';
import {
  formatClock,
  formatCount,
  formatDate,
  formatDateTime,
  formatDateTimeFull,
  formatDuration,
  formatDurationBetween,
  formatJitter,
  formatLatency,
  formatLossPct,
  formatMOS,
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

test('call-quality figures: MOS to two decimals, loss as the worker prints it, fine jitter', () => {
  assert.equal(formatMOS(4.3), '4.30');
  assert.equal(formatMOS(4.314), '4.31');
  assert.equal(formatMOS(undefined), '—');
  assert.equal(formatMOS(Number.NaN), '—');

  assert.equal(formatLossPct(0), '0%');
  assert.equal(formatLossPct(0.44), '0.44%');
  assert.equal(formatLossPct(5), '5.00%');
  assert.equal(formatLossPct(-1), '—');
  assert.equal(formatLossPct(null), '—');

  assert.equal(formatJitter(2.14), '2.1 ms');
  assert.equal(formatJitter(0), '0.0 ms');
  // 9.96 rounds to 10.0: past the fine band, so whole milliseconds.
  assert.equal(formatJitter(9.96), '10 ms');
  assert.equal(formatJitter(34.4), '34 ms');
  assert.equal(formatJitter(undefined), '—');
});

test('formatCount groups thousands the same on every locale', () => {
  assert.equal(formatCount(0), '0');
  assert.equal(formatCount(999), '999');
  assert.equal(formatCount(1459), '1,459');
  assert.equal(formatCount(1234567.4), '1,234,567');
  assert.equal(formatCount(-12000), '-12,000');
  assert.equal(formatCount(null), '—');
  assert.equal(formatCount(Number.NaN), '—');
});

test('formatClock is the time half of formatTimestamp', () => {
  const value = '2026-10-01T09:05:42Z';
  assert.equal(formatClock(value, { tz: 'utc' }), '09:05');
  assert.equal(formatClock(value, { tz: 'utc', seconds: true }), '09:05:42');
  assert.ok(formatTimestamp(value).endsWith(` ${formatClock(value, { seconds: true })}`));
  assert.equal(formatClock(undefined), '—');
  assert.equal(formatClock('garbage', {}, 'garbage'), 'garbage');
});
