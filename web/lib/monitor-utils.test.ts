import assert from 'node:assert/strict';
import test from 'node:test';
import {
  calculateLatencyStats,
  displayValueLabel,
  getEffectiveMonitorStatus,
  getHTTPDisplayValues,
  monitorTarget,
  shortFailureReason,
  sortBySeverity,
  STATUS_RANK,
} from './monitor-utils';
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

test('latency stats ignore failed checks', () => {
  const results = [
    { id: 'a', status: 'failure', result_source: 'monitor', latency_ms: 10000, created_at: '' },
    { id: 'b', status: 'success', result_source: 'monitor', latency_ms: 60, created_at: '' },
    { id: 'c', status: 'success', result_source: 'monitor', latency_ms: 40, created_at: '' },
  ] as unknown as CheckResult[];
  const stats = calculateLatencyStats(results);
  assert.equal(stats.median, 50);
  assert.equal(stats.latest, 60);
  assert.equal(stats.samples, 2);
  assert.deepEqual(calculateLatencyStats(failureResults()), { median: 0, p95: 0, latest: 0, samples: 0 });
});

test('sortBySeverity puts problems first, then name', () => {
  const items = [
    { name: 'b', s: 'up' },
    { name: 'a', s: 'paused' },
    { name: 'c', s: 'down' },
    { name: 'a', s: 'up' },
  ] as const;
  const sorted = sortBySeverity(items, (i) => i.s, (i) => i.name).map((i) => `${i.s}:${i.name}`);
  assert.deepEqual(sorted, ['down:c', 'up:a', 'up:b', 'paused:a']);
  assert.ok(STATUS_RANK.down < STATUS_RANK.degraded);
});

test('monitorTarget mirrors the list and detail target', () => {
  assert.equal(monitorTarget({ type: 'http', config: { url: 'https://x.test/h' } as never }), 'https://x.test/h');
  assert.equal(monitorTarget({ type: 'grpc', config: { host: 'g.test' } as never }), 'g.test:443');
  assert.equal(monitorTarget({ type: 'tcp', config: { host: 'db', port: 5432 } as never }), 'db:5432');
  assert.equal(monitorTarget({ type: 'ping', config: { host: '10.0.0.1' } as never }), '10.0.0.1');
  assert.equal(monitorTarget({ type: 'push', config: {} as never }), null);
  assert.equal(monitorTarget({ type: 'sip', config: { host: 'sbc.test', port: 5080 } as never }), 'sbc.test:5080');
  assert.equal(monitorTarget({ type: 'sip', config: { host: 'sbc.test', transport: 'TLS' } as never }), 'sbc.test:5061');
  assert.equal(monitorTarget({ type: 'sip', config: { host: 'x.test', resolution: 'rfc3263', port: 0 } as never }), 'x.test');
  assert.equal(
    monitorTarget({ type: 'sip', config: { host: 'x.test', resolution: 'rfc3263', port: 5070 } as never }),
    'x.test:5070'
  );
});

test('shortFailureReason strips Go framing', () => {
  assert.equal(
    shortFailureReason('connect: Get "http://localhost:9999/": dial tcp [::1]:9999: connect: connection refused'),
    'connection refused'
  );
  assert.equal(
    shortFailureReason('timeout: Get "https://x.test": context deadline exceeded (Client.Timeout exceeded while awaiting headers)', {
      timeoutSeconds: 10,
    }),
    'timed out after 10 s'
  );
  assert.equal(shortFailureReason('dns: Get "https://nope.test": dial tcp: lookup nope.test on 1.1.1.1:53: no such host'), 'no such host');
  assert.equal(shortFailureReason('status_code: got 503'), 'HTTP 503');
  assert.equal(shortFailureReason('status_code: got 503; latency: 1200ms > 1000ms'), 'HTTP 503 + latency');
  assert.equal(shortFailureReason('latency: 1200ms > 1000ms'), 'slow response (1.2 s, limit 1.0 s)');
  assert.equal(
    shortFailureReason('tls: failed to verify certificate: x509: certificate has expired or is not yet valid'),
    'TLS certificate expired'
  );
  assert.equal(shortFailureReason('keyword "ok" not found'), 'keyword "ok" not found');
  // Assertion messages quote the operator's values; never read them as transport errors.
  assert.equal(shortFailureReason('body: missing "timeout"'), 'body: missing "timeout"');
  assert.equal(shortFailureReason('body: contains "connection refused"'), 'body: contains "connection refused"');
  assert.equal(
    shortFailureReason('body: regex matched "certificate has expired"'),
    'body: regex matched "certificate has expired"'
  );
  assert.equal(shortFailureReason('header X-Status: != "timeout"'), 'header X-Status: != "timeout"');
  assert.equal(shortFailureReason('', { httpStatus: 502 }), 'HTTP 502');
  assert.equal(shortFailureReason(null), '');
});
