import assert from 'node:assert/strict';
import test from 'node:test';
import { getEffectiveMonitorStatus } from './monitor-utils';
import type { CheckResult } from './types';

function successResult(): CheckResult {
  return {
    id: 'r1',
    status: 'success',
    result_source: 'monitor',
    metrics_data: {},
    created_at: new Date().toISOString(),
  } as unknown as CheckResult;
}

function failureResults(): CheckResult[] {
  return Array.from({ length: 5 }, (_, i) => ({
    id: `r${i}`,
    status: 'failure',
    result_source: 'monitor',
    metrics_data: {},
    created_at: new Date().toISOString(),
  })) as unknown as CheckResult[];
}

test('paused wins over maintenance and results', () => {
  const status = getEffectiveMonitorStatus(
    { enabled: false, in_maintenance: true },
    failureResults()
  );
  assert.equal(status, 'paused');
});

test('maintenance wins over down', () => {
  const status = getEffectiveMonitorStatus(
    { enabled: true, in_maintenance: true },
    failureResults()
  );
  assert.equal(status, 'maintenance');
});

test('maintenance wins over up', () => {
  const status = getEffectiveMonitorStatus(
    { enabled: true, in_maintenance: true },
    [successResult()]
  );
  assert.equal(status, 'maintenance');
});

test('without maintenance flag the latest result decides', () => {
  assert.equal(
    getEffectiveMonitorStatus({ enabled: true, in_maintenance: false }, [successResult()]),
    'up'
  );
  assert.equal(
    getEffectiveMonitorStatus({ enabled: true }, failureResults()),
    'down'
  );
});
