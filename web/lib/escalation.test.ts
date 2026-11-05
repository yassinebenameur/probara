import assert from 'node:assert/strict';
import test from 'node:test';
import {
  describeDelay, describeRepeat, hasPeople, makeTarget, policyInputError, targetRef, toPolicyInput,
  type EscalationPolicyInput,
} from './escalation';

const base = (): EscalationPolicyInput => ({
  name: 'Primary',
  repeat_count: 0,
  repeat_after_seconds: 1800,
  steps: [
    { delay_seconds: 0, targets: [makeTarget('user', 'u1')] },
    { delay_seconds: 600, targets: [makeTarget('schedule', 's1'), { kind: 'channel', channel_id: 'c1', notify_after_ack: true }] },
  ],
});

test('a well-formed policy passes', () => {
  assert.equal(policyInputError(base()), null);
});

test('policy validation names the step at fault', () => {
  const decreasing = base();
  decreasing.steps[1].delay_seconds = 0;
  decreasing.steps[0].delay_seconds = 60;
  assert.match(policyInputError(decreasing) ?? '', /^Step 2: its delay/);

  const empty = base();
  empty.steps[1].targets = [];
  assert.match(policyInputError(empty) ?? '', /^Step 2: add at least one target/);

  const unchosen = base();
  unchosen.steps[0].targets = [makeTarget('user', '')];
  assert.match(policyInputError(unchosen) ?? '', /^Step 1: choose a person/);

  const twice = base();
  twice.steps[0].targets.push({ kind: 'channel', channel_id: 'c1' });
  assert.match(policyInputError(twice) ?? '', /already in step 1/);

  const repeat = base();
  repeat.repeat_after_seconds = 10;
  assert.match(policyInputError(repeat) ?? '', /pause before a repeat/);

  assert.equal(policyInputError({ ...base(), name: ' ' }), 'Name the policy.');
});

test('targets carry the id their kind needs', () => {
  assert.equal(targetRef(makeTarget('schedule', 's9')), 's9');
  assert.deepEqual(makeTarget('all_admins', 'ignored'), { kind: 'all_admins' });
  assert.equal(hasPeople(base().steps), true);
  assert.equal(hasPeople([{ delay_seconds: 0, targets: [makeTarget('channel', 'c')] }]), false);
});

test('the write payload drops read-only fields and person-only notify_after_ack', () => {
  const input = base();
  input.steps[0].targets[0] = { ...input.steps[0].targets[0], label: 'alice', reachable: true, notify_after_ack: true };
  const out = toPolicyInput(input);
  assert.deepEqual(out.steps[0].targets[0], { kind: 'user', user_id: 'u1' });
  assert.deepEqual(out.steps[1].targets[1], { kind: 'channel', channel_id: 'c1', notify_after_ack: true });
});

test('delays and repeats read as sentences', () => {
  assert.equal(describeDelay(0), 'Immediately');
  assert.equal(describeDelay(300), 'After 5m');
  assert.equal(describeRepeat({ repeat_count: 0, repeat_after_seconds: 1800 }), 'People are paged once per step');
  assert.equal(describeRepeat({ repeat_count: 2, repeat_after_seconds: 1800 }), 'People steps repeat 2 more times, 30m after the last step');
});
