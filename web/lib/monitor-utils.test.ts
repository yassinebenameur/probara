import assert from 'node:assert/strict';
import test from 'node:test';
import { displayValueLabel, getEffectiveMonitorStatus, getHTTPDisplayValues } from './monitor-utils';
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

test('display values come from the latest non-platform result', () => {
  const results = [
    { id: 'p', status: 'error', result_source: 'platform', created_at: '' },
    {
      id: 'm',
      status: 'success',
      result_source: 'monitor',
      metrics_data: {
        http: {
          display_values: [
            { label: 'ver', path: 'version', value: '1.2.3' },
            { path: 'checks.db', value: 'ok' },
            { path: 'broken' },
          ],
        },
      },
      created_at: '',
    },
  ] as unknown as CheckResult[];
  const values = getHTTPDisplayValues(results);
  assert.deepEqual(values.map(displayValueLabel), ['ver', '']);
  assert.deepEqual(values.map((v) => v.value), ['1.2.3', 'ok']);
});

test('no display values without http metrics', () => {
  assert.deepEqual(getHTTPDisplayValues([successResult()]), []);
  assert.deepEqual(getHTTPDisplayValues([]), []);
});
