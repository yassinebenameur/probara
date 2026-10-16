import assert from 'node:assert/strict';
import test from 'node:test';
import { buildClonedMonitorInitialData } from './monitor-clone';
import type { Monitor, PrometheusMonitorConfig, SIPMonitorConfig } from './types';

test('Prometheus clone retains query and threshold but drops masked credentials', () => {
  const source = {
    id: 'source', name: 'Queue', type: 'prometheus', interval_seconds: 60, timeout_seconds: 10,
    enabled: true, tags: ['production'], location_ids: ['location'], location_quorum: 1,
    config: { url: 'https://prom.example.com', query: 'sum(queue_depth)', operator: 'lt', threshold: 0,
      auth_type: 'bearer', bearer_token: '***', password: '***', no_data_status: 'error' },
  } as Monitor;
  const clone = buildClonedMonitorInitialData(source);
  const config = clone.config as PrometheusMonitorConfig;
  assert.equal(clone.type, 'prometheus');
  assert.equal(config.query, 'sum(queue_depth)');
  assert.equal(config.threshold, 0);
  assert.equal(config.no_data_status, 'error');
  assert.equal(config.bearer_token, undefined);
  assert.equal(config.password, undefined);
  assert.equal((source.config as PrometheusMonitorConfig).bearer_token, '***');
});

test('SIP clone keeps every non-secret field and drops the masked password', () => {
  const source = {
    id: 'source', name: 'Trunk', type: 'sip', interval_seconds: 60, timeout_seconds: 10,
    enabled: true, tags: ['voice'], location_ids: [], location_quorum: 1,
    config: {
      host: 'sbc.example.com', port: 5061, transport: 'tls', method: 'register',
      username: 'agent42', password: '***', domain: 'example.com', expected_status: 200,
      tls_skip_verify: true, tls_server_name: 'sip.example.com',
      future_field: { nested: ['kept'] },
    },
  } as unknown as Monitor;
  const clone = buildClonedMonitorInitialData(source);
  const config = clone.config as SIPMonitorConfig & { future_field?: unknown };
  assert.equal(clone.type, 'sip');
  assert.deepEqual(config, {
    host: 'sbc.example.com', port: 5061, transport: 'tls', method: 'register',
    username: 'agent42', domain: 'example.com', expected_status: 200,
    tls_skip_verify: true, tls_server_name: 'sip.example.com',
    future_field: { nested: ['kept'] },
  });
  assert.equal((source.config as SIPMonitorConfig).password, '***');
  // Deep copy: editing the clone never reaches the source monitor.
  (config.future_field as { nested: string[] }).nested.push('x');
  assert.deepEqual((source.config as unknown as { future_field: { nested: string[] } }).future_field.nested, ['kept']);
});
