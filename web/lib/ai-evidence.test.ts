import assert from 'node:assert/strict';
import { test } from 'node:test';
import { evidenceRefLabel, withMonitorNames } from './ai-evidence';

const A = 'be5ffc63-b6c4-4e39-958f-8074b268d729';
const B = 'f4ea7197-4434-4035-931e-86b492910636';
const names = { [A]: 'Returns & exchanges portal' };

test('withMonitorNames replaces known monitor IDs and keeps the rest', () => {
  assert.equal(withMonitorNames(`${A}: configured upstream ${B} unavailable`, names), `Returns & exchanges portal: configured upstream ${B} unavailable`);
  assert.equal(withMonitorNames(`${A}: missing checks`), `${A}: missing checks`);
  assert.equal(withMonitorNames('Multiple supported hypotheses', names), 'Multiple supported hypotheses');
});

test('evidenceRefLabel names the monitor and keeps the facet', () => {
  assert.equal(evidenceRefLabel(`${A}/checks`, A, names), 'Returns & exchanges portal · checks');
  assert.equal(evidenceRefLabel(A, A, names), 'Returns & exchanges portal');
  assert.equal(evidenceRefLabel(`${B}/checks`, B, names), `${B}/checks`);
  assert.equal(evidenceRefLabel(`${A}/checks`, undefined, names), `${A}/checks`);
});
