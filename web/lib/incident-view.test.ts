import assert from 'node:assert/strict';
import test from 'node:test';
import {
  countTimeline,
  groupTimeline,
  minutesSinceOpen,
  sameIdSet,
  sortTimeline,
  timelineClock,
} from './incident-view';
import { formatTimestamp } from './format';
import type { IncidentTimelineEntry } from './types';

let seq = 0;
function entry(
  entry_type: IncidentTimelineEntry['entry_type'],
  message: string,
  created_at: string,
): IncidentTimelineEntry {
  seq += 1;
  return { id: `e${seq}`, tenant_id: 't', incident_id: 'i', entry_type, message, created_at };
}

// Day key from the UTC date, so the tests do not depend on the runner's zone.
const utcDay = (value: string) => value.slice(0, 10);

test('sortTimeline orders newest first without mutating its input', () => {
  const a = entry('system', 'a', '2026-10-01T10:00:00Z');
  const b = entry('system', 'b', '2026-10-01T12:00:00Z');
  const input = [a, b];
  assert.deepEqual(sortTimeline(input).map((e) => e.message), ['b', 'a']);
  assert.deepEqual(input.map((e) => e.message), ['a', 'b']);
});

test('adjacent identical system events fold into one row spanning the run', () => {
  const sorted = sortTimeline([
    entry('system', 'Alert attached', '2026-10-01T10:00:00Z'),
    entry('system', 'Alert attached', '2026-10-01T10:01:00Z'),
    entry('system', 'Alert attached', '2026-10-01T10:02:00Z'),
    entry('system', 'Incident created', '2026-10-01T09:59:00Z'),
  ]);
  const [day] = groupTimeline(sorted, 'all', utcDay);
  assert.equal(day.rows.length, 2);
  assert.equal(day.rows[0].count, 3);
  assert.equal(day.rows[0].entry.created_at, '2026-10-01T10:02:00Z');
  assert.equal(day.rows[0].first.created_at, '2026-10-01T10:00:00Z');
  assert.equal(day.rows[1].count, 1);
});

test('notes and public updates never fold, even when identical', () => {
  const sorted = sortTimeline([
    entry('internal_note', 'same', '2026-10-01T10:00:00Z'),
    entry('internal_note', 'same', '2026-10-01T10:01:00Z'),
    entry('public_update', 'same', '2026-10-01T10:02:00Z'),
    entry('public_update', 'same', '2026-10-01T10:03:00Z'),
  ]);
  const [day] = groupTimeline(sorted, 'all', utcDay);
  assert.equal(day.rows.length, 4);
  assert.ok(day.rows.every((row) => row.count === 1));
});

test('folding stops at a day boundary', () => {
  const sorted = sortTimeline([
    entry('system', 'Alert attached', '2026-10-01T23:59:00Z'),
    entry('system', 'Alert attached', '2026-10-02T00:01:00Z'),
  ]);
  const days = groupTimeline(sorted, 'all', utcDay);
  assert.deepEqual(days.map((d) => d.key), ['2026-10-02', '2026-10-01']);
  assert.deepEqual(days.map((d) => d.rows.length), [1, 1]);
});

test('a filter never merges events that were separated by hidden entries', () => {
  const sorted = sortTimeline([
    entry('system', 'Incident moved to monitoring', '2026-10-01T10:00:00Z'),
    entry('internal_note', 'checking the fix', '2026-10-01T10:05:00Z'),
    entry('system', 'Incident moved to monitoring', '2026-10-01T10:10:00Z'),
  ]);
  const [day] = groupTimeline(sorted, 'system', utcDay);
  assert.equal(day.rows.length, 2);
  assert.ok(day.rows.every((row) => row.count === 1));

  const notes = groupTimeline(sorted, 'internal_note', utcDay);
  assert.equal(notes.length, 1);
  assert.deepEqual(notes[0].rows.map((r) => r.entry.message), ['checking the fix']);
});

test('a filter drops days left without rows', () => {
  const sorted = sortTimeline([
    entry('system', 'Incident created', '2026-10-01T10:00:00Z'),
    entry('public_update', 'Investigating', '2026-10-02T10:00:00Z'),
  ]);
  const days = groupTimeline(sorted, 'public_update', utcDay);
  assert.deepEqual(days.map((d) => d.key), ['2026-10-02']);
  assert.deepEqual(groupTimeline([], 'all', utcDay), []);
});

test('countTimeline counts each entry type and the total', () => {
  const counts = countTimeline([
    entry('system', 'x', '2026-10-01T10:00:00Z'),
    entry('system', 'x', '2026-10-01T10:01:00Z'),
    entry('internal_note', 'n', '2026-10-01T10:02:00Z'),
  ]);
  assert.deepEqual(counts, { all: 3, system: 2, internal_note: 1, public_update: 0 });
});

test('timelineClock is the HH:mm of the table timestamp', () => {
  const value = '2026-10-01T09:05:42Z';
  const clock = timelineClock(value);
  assert.match(clock, /^\d{2}:\d{2}$/);
  // formatClock is the time half of formatTimestamp, so the two never disagree.
  assert.ok(formatTimestamp(value, { seconds: false }).endsWith(` ${clock}`));
  // Unparseable input falls through unchanged rather than being cut short.
  assert.equal(timelineClock('garbage'), 'garbage');
});

test('minutesSinceOpen floors to whole minutes and hides the first minute', () => {
  const open = '2026-10-01T10:00:00Z';
  assert.equal(minutesSinceOpen('2026-10-01T10:00:59Z', open), null);
  assert.equal(minutesSinceOpen('2026-10-01T09:59:00Z', open), null);
  assert.equal(minutesSinceOpen('2026-10-01T10:01:00Z', open), 1);
  assert.equal(minutesSinceOpen('2026-10-01T10:21:59Z', open), 21);
  assert.equal(minutesSinceOpen('nope', open), null);
});

test('sameIdSet ignores order and duplicates', () => {
  assert.equal(sameIdSet(['a', 'b'], ['b', 'a']), true);
  assert.equal(sameIdSet([], []), true);
  assert.equal(sameIdSet(['a', 'b'], ['a']), false);
  assert.equal(sameIdSet(['a', 'b'], ['a', 'a']), false);
  assert.equal(sameIdSet(['a', 'a'], ['a', 'b']), false);
  assert.equal(sameIdSet(['a', 'a', 'b'], ['b', 'a']), true);
});
