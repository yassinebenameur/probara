import assert from 'node:assert/strict';
import test from 'node:test';
import { relapseChip, relapseCount, relapseLine, relapseTitle, reopenCooldownOptions } from './alert-relapse';

test('a relapsed alert gets a counted chip; others none', () => {
  assert.equal(relapseChip({ relapse_count: 3 }), 'Relapsed ×3');
  assert.equal(relapseChip({ relapse_count: 1 }), 'Relapsed ×1');
  assert.equal(relapseChip({ relapse_count: 0 }), null);
  assert.equal(relapseChip({}), null);
  assert.equal(relapseCount({ relapse_count: -1 }), 0);
});

test('the chip title says how often and when last', () => {
  assert.equal(
    relapseTitle({ relapse_count: 2 }, '12:04'),
    'Failed again soon after recovering, so this alert reopened 2 times instead of a new one opening. Last reopened 12:04.'
  );
  assert.match(relapseTitle({ relapse_count: 1 }, null), /reopened 1 time instead/);
});

test('cooldown options keep a stored value outside the presets', () => {
  const presets = reopenCooldownOptions(15).map((o) => o.minutes);
  assert.deepEqual(presets, [0, 5, 15, 30, 60, 240]);
  const custom = reopenCooldownOptions(90);
  assert.deepEqual(
    custom.map((o) => o.minutes),
    [0, 5, 15, 30, 60, 90, 240]
  );
  assert.equal(custom.find((o) => o.minutes === 90)?.label, '1h 30m');
  assert.deepEqual(reopenCooldownOptions(0).map((o) => o.minutes), presets);
});

test('a history line gives the gap the relapse followed', () => {
  assert.equal(
    relapseLine({ number: 2, recovered_at: '2026-11-01T10:00:00Z', reopened_at: '2026-11-01T10:04:00Z' }),
    '#2 · down again 4m after recovering'
  );
});
