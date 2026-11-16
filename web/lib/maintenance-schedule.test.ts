import assert from 'node:assert/strict';
import test from 'node:test';
import {
  describeRecurrence,
  describeWeekdays,
  formatCalendarDate,
  recurrenceInputError,
  sameRecurrence,
  splitMinutes,
  WEEKDAY_OPTIONS,
} from './maintenance-schedule';
import type { MaintenanceRecurrence } from './types/maintenance';

const NOW = new Date(2026, 10, 15);

const rule = (over: Partial<MaintenanceRecurrence> = {}): MaintenanceRecurrence => ({
  frequency: 'weekly',
  weekdays: [2, 4],
  start_time: '02:00',
  duration_minutes: 90,
  timezone: 'Europe/Paris',
  ...over,
});

test('weekday chips run Monday first with the API values', () => {
  assert.deepEqual(WEEKDAY_OPTIONS.map((o) => o.value), [1, 2, 3, 4, 5, 6, 0]);
  assert.equal(WEEKDAY_OPTIONS[6].label, 'Sun');
});

test('weekdays read as words', () => {
  assert.equal(describeWeekdays([0, 1, 2, 3, 4, 5, 6]), 'every day');
  assert.equal(describeWeekdays([5, 4, 3, 2, 1]), 'weekdays');
  assert.equal(describeWeekdays([0, 6]), 'weekends');
  assert.equal(describeWeekdays([4, 2]), 'Tue and Thu');
  assert.equal(describeWeekdays([0, 1, 3]), 'Mon, Wed and Sun');
});

test('a rule reads as one line', () => {
  assert.equal(describeRecurrence(rule(), NOW), 'Every Tue and Thu at 02:00 Europe/Paris, for 1h 30m');
  assert.equal(
    describeRecurrence(rule({ weekdays: [0, 1, 2, 3, 4, 5, 6], until: '2026-12-31' }), NOW),
    'Every day at 02:00 Europe/Paris, for 1h 30m, until 31 Dec'
  );
  assert.equal(describeRecurrence(rule({ weekdays: [1, 2, 3, 4, 5], until: '2027-01-15' }), NOW),
    'On weekdays at 02:00 Europe/Paris, for 1h 30m, until 15 Jan 2027');
  assert.equal(describeRecurrence({ frequency: 'none' }, NOW), 'Once');
});

test('calendar dates are not shifted by the browser zone', () => {
  assert.equal(formatCalendarDate('2026-03-29', NOW), '29 Mar');
  assert.equal(formatCalendarDate('nonsense', NOW), 'nonsense');
});

test('the form catches what it can before the API does', () => {
  assert.equal(recurrenceInputError(rule()), null);
  assert.match(recurrenceInputError(rule({ weekdays: [] })) ?? '', /at least one day/);
  assert.match(recurrenceInputError(rule({ start_time: '' })) ?? '', /start time/);
  assert.match(recurrenceInputError(rule({ start_time: '24:00' })) ?? '', /start time/);
  assert.match(recurrenceInputError(rule({ duration_minutes: 0 })) ?? '', /at least a minute/);
  assert.match(recurrenceInputError(rule({ duration_minutes: 7 * 24 * 60 + 1 })) ?? '', /7 days/);
  assert.match(recurrenceInputError(rule({ timezone: ' ' })) ?? '', /timezone/);
});

test('rules compare by schedule, not by weekday order', () => {
  assert.ok(sameRecurrence(rule(), rule({ weekdays: [4, 2] })));
  assert.ok(!sameRecurrence(rule(), rule({ duration_minutes: 60 })));
  assert.ok(!sameRecurrence(rule(), rule({ until: '2027-01-01' })));
  assert.ok(!sameRecurrence(rule(), undefined));
  assert.ok(sameRecurrence(undefined, undefined));
});

test('minutes split into the form fields', () => {
  assert.deepEqual(splitMinutes(90), { hours: 1, minutes: 30 });
  assert.deepEqual(splitMinutes(45), { hours: 0, minutes: 45 });
});
