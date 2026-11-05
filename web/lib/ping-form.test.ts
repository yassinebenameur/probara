import assert from 'node:assert/strict';
import test from 'node:test';
import { buildMonitorFormRequest, defaultTimeoutFor, initialMonitorFormState, validateMonitorForm } from './monitor-form/inline';
import type { MonitorFormState } from './monitor-form/inline';
import { storedMonitor } from './monitor-form/testing';
import { latestPingMetrics, pingFacts } from './ping-metrics';
import type { CheckResult } from './types';

function freshPing(patch: Partial<MonitorFormState> = {}): MonitorFormState {
  return { ...initialMonitorFormState(), timeout_seconds: defaultTimeoutFor('ping'), name: 'Edge', host: 'edge.example.com', ...patch };
}

test('ping: a new monitor sends three echoes', () => {
  const req = buildMonitorFormRequest('ping', freshPing(), false);
  assert.deepEqual(req.config, { host: 'edge.example.com', count: 3 });
});

test('ping: every field round-trips', () => {
  const config = { host: 'gw.internal', count: 5, interval_ms: 250, packet_size: 56, max_packet_loss_pct: 20.5, max_rtt_ms: 150, warn_rtt_ms: 80 };
  const form = initialMonitorFormState(storedMonitor('ping', config));
  assert.deepEqual(validateMonitorForm('ping', form, false), {});
  assert.deepEqual(buildMonitorFormRequest('ping', form, false).config, config);
});

test('ping: a stored single-echo config saves back without a count', () => {
  const form = initialMonitorFormState(storedMonitor('ping', { host: 'gw.internal' }));
  assert.equal(form.ping_count, '');
  assert.deepEqual(buildMonitorFormRequest('ping', form, false).config, { host: 'gw.internal' });
});

test('ping: bounds and timeout fit', () => {
  assert.deepEqual(
    validateMonitorForm(
      'ping',
      freshPing({
        ping_count: '11',
        ping_interval_ms: '100',
        ping_packet_size: '20',
        ping_max_packet_loss_pct: '100',
        ping_max_rtt_ms: '0',
        ping_warn_rtt_ms: 'x',
      }),
      false
    ),
    {
      ping_count: 'Must be a whole number from 1 to 10',
      ping_interval_ms: 'Must be 200–10000 ms',
      ping_packet_size: 'Must be 24–1472 bytes',
      ping_max_packet_loss_pct: 'Must be 0 or more and below 100',
      ping_max_rtt_ms: 'Must be a whole number of ms above 0',
      ping_warn_rtt_ms: 'Must be a whole number of ms above 0',
    }
  );
  assert.deepEqual(validateMonitorForm('ping', freshPing({ ping_max_rtt_ms: '100', ping_warn_rtt_ms: '100' }), false), {
    ping_warn_rtt_ms: 'Must be below the failure threshold',
  });
  assert.deepEqual(validateMonitorForm('ping', freshPing({ ping_count: '10', timeout_seconds: 5 }), false), {
    timeout_seconds: '10 echoes 1000 ms apart need a timeout of at least 10s',
  });
});

test('ping: detail facts', () => {
  const results = [
    { metrics_data: { http: {} } },
    { metrics_data: { ping: { host: 'gw', ip: '192.0.2.1', sent: 3, received: 2, loss_pct: 33.33, rtt_min_ms: 0.42, rtt_avg_ms: 12.5, rtt_max_ms: 30, rtt_warn_ms: 10 } } },
  ] as unknown as CheckResult[];
  const m = latestPingMetrics(results);
  assert.ok(m);
  assert.deepEqual(pingFacts(m, 20), [
    { key: 'ip', label: 'Address', value: '192.0.2.1' },
    { key: 'loss', label: 'Packet loss', value: '33.3% (2/3 answered)', tone: 'warning' },
    { key: 'rtt', label: 'Round trip', value: 'min 0.4 ms · avg 13 ms · max 30 ms' },
    { key: 'rtt-warn', label: 'Warning', value: 'average round trip over 10 ms', tone: 'warning' },
  ]);
  assert.deepEqual(pingFacts({ sent: 1, received: 0, loss_pct: 100 }), [
    { key: 'loss', label: 'Packet loss', value: '100% (0/1 answered)', tone: 'warning' },
  ]);
});
