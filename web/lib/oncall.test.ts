import assert from 'node:assert/strict';
import test from 'node:test';
import {
  describeRotation, feedURL, formatZoned, isoToZonedLocal, scheduleInputError, zonedLocalToISO, type ScheduleInput,
} from './oncall';

test('rotations describe their cadence', () => {
  assert.equal(describeRotation({ kind: 'daily', handoff_time: '09:00' }), 'Daily at 09:00');
  assert.equal(describeRotation({ kind: 'weekly', handoff_time: '10:30', handoff_weekday: 1 }), 'Weekly, Monday 10:30');
});

test('wall-clock times convert in the schedule zone, across DST', () => {
  // Paris is UTC+1 in winter, UTC+2 in summer.
  assert.equal(zonedLocalToISO('2026-03-28T09:00', 'Europe/Paris'), '2026-03-28T08:00:00.000Z');
  assert.equal(zonedLocalToISO('2026-03-29T09:00', 'Europe/Paris'), '2026-03-29T07:00:00.000Z');
  assert.equal(zonedLocalToISO('2026-11-02T10:00', 'America/New_York'), '2026-11-02T15:00:00.000Z');
  assert.equal(zonedLocalToISO('2026-06-01T00:00', 'UTC'), '2026-06-01T00:00:00.000Z');
  assert.equal(zonedLocalToISO('not a time', 'UTC'), null);
  assert.equal(isoToZonedLocal('2026-03-29T07:00:00Z', 'Europe/Paris'), '2026-03-29T09:00');
  assert.equal(isoToZonedLocal('2026-11-02T15:00:00Z', 'America/New_York'), '2026-11-02T10:00');
  assert.equal(formatZoned('2026-11-02T15:00:00Z', 'America/New_York'), 'Mon 2 Nov, 10:00');
});

test('schedule input validation names the rotation at fault', () => {
  const base: ScheduleInput = {
    name: 'Primary',
    timezone: 'UTC',
    rotations: [{ kind: 'daily', handoff_time: '09:00', start_at: '2026-01-01T00:00:00Z', member_ids: ['a'] }],
  };
  assert.equal(scheduleInputError(base), null);
  assert.equal(scheduleInputError({ ...base, name: ' ' }), 'Name the schedule.');
  assert.equal(scheduleInputError({ ...base, rotations: [] }), 'Add at least one rotation.');
  assert.equal(
    scheduleInputError({ ...base, rotations: [{ ...base.rotations[0], kind: 'weekly' }] }),
    'Choose the handoff day.',
  );
  assert.equal(
    scheduleInputError({ ...base, rotations: [base.rotations[0], { ...base.rotations[0], member_ids: [] }] }),
    'Rotation 2: add at least one member.',
  );
  assert.equal(
    scheduleInputError({ ...base, rotations: [{ ...base.rotations[0], handoff_time: '25:00' }] }),
    'Set a handoff time (HH:MM).',
  );
});

test('feed URLs prefer the API public base URL', () => {
  assert.equal(feedURL({ token: 't', path: '/api/v1/oncall-feeds/t.ics', url: 'https://api.example.com/api/v1/oncall-feeds/t.ics' }, 'https://app'), 'https://api.example.com/api/v1/oncall-feeds/t.ics');
  assert.equal(feedURL({ token: 't', path: '/api/v1/oncall-feeds/t.ics' }, 'https://app.example.com/'), 'https://app.example.com/api/v1/oncall-feeds/t.ics');
});
