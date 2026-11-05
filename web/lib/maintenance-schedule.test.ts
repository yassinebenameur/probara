import assert from 'node:assert/strict';
import test from 'node:test';
import { describeRecurrence, isoToZonedWall, zonedWallToISO } from './maintenance-schedule';
import { formatDateTimeInZone } from './format';

test('zonedWallToISO reads the wall time in the rule zone, not the browser', () => {
  assert.equal(zonedWallToISO('2026-01-15T02:00', 'Europe/Paris'), '2026-01-15T01:00:00.000Z');
  assert.equal(zonedWallToISO('2026-07-15T02:00', 'Europe/Paris'), '2026-07-15T00:00:00.000Z');
  assert.equal(zonedWallToISO('2026-07-15T02:00', 'America/New_York'), '2026-07-15T06:00:00.000Z');
  assert.equal(zonedWallToISO('2026-07-15T02:00', 'UTC'), '2026-07-15T02:00:00.000Z');
  // Skipped by the spring-forward gap: resolves forward (03:30 CEST).
  assert.equal(zonedWallToISO('2026-03-29T02:30', 'Europe/Paris'), '2026-03-29T01:30:00.000Z');
  // Repeated at fall-back: the first instance (CEST).
  assert.equal(zonedWallToISO('2026-10-25T02:30', 'Europe/Paris'), '2026-10-25T00:30:00.000Z');
  assert.equal(zonedWallToISO('nope', 'UTC'), '');
});

test('isoToZonedWall round-trips', () => {
  for (const zone of ['Europe/Paris', 'Asia/Kolkata', 'America/Los_Angeles', 'UTC']) {
    const wall = '2026-11-08T02:15';
    assert.equal(isoToZonedWall(zonedWallToISO(wall, zone), zone), wall, zone);
  }
});

test('describeRecurrence names the rule', () => {
  assert.equal(describeRecurrence({ frequency: 'none' }), '');
  assert.equal(describeRecurrence({ frequency: 'daily', interval: 1 }), 'Daily');
  assert.equal(describeRecurrence({ frequency: 'daily', interval: 3 }), 'Every 3 days');
  assert.equal(describeRecurrence({ frequency: 'weekly', weekdays: [0, 4, 1] }), 'Weekly on Mon, Thu, Sun');
  assert.equal(describeRecurrence({ frequency: 'monthly', month_day: 31 }), 'Monthly on the 31st (last day in shorter months)');
  assert.equal(describeRecurrence({ frequency: 'monthly', interval: 2, month_day: 2 }), 'Every 2 months on the 2nd');
  assert.equal(describeRecurrence({ frequency: 'cron', cron: '0 2 * * 0' }), 'Cron 0 2 * * 0');
});

test('formatDateTimeInZone formats in the named zone', () => {
  const now = new Date('2026-11-05T12:00:00Z');
  assert.equal(formatDateTimeInZone('2026-11-08T01:00:00Z', 'Europe/Paris', '—', now), 'Sun 8 Nov, 02:00');
  assert.equal(formatDateTimeInZone('2027-01-01T00:30:00Z', 'UTC', '—', now), 'Fri 1 Jan 2027, 00:30');
});
