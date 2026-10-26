import assert from 'node:assert/strict';
import test from 'node:test';
import { buildClonedMonitorInitialData } from './monitor-clone';
import { buildWebsocketRequest, initialWebsocketFormState } from './monitor-form/websocket';
import { MASKED_SECRET } from './types';
import type { Monitor, MonitorType, PrometheusMonitorConfig, SIPMonitorConfig, WebSocketMonitorConfig } from './types';

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

test('SIP test-call clone keeps the whole call', () => {
  const call = { number: '+33123456789', mode: 'ring', warn_pdd_ms: 3000, probe_header: true };
  const source = {
    id: 'source', name: 'IVR', type: 'sip', interval_seconds: 300, timeout_seconds: 15,
    enabled: true, tags: [], location_ids: [], location_quorum: 1,
    config: { host: 'trunk.example.com', port: 5060, transport: 'udp', method: 'invite', username: 'u', password: '***', call },
  } as unknown as Monitor;
  const config = buildClonedMonitorInitialData(source).config as SIPMonitorConfig;
  assert.equal(config.method, 'invite');
  assert.deepEqual(config.call, call);
  assert.equal(config.password, undefined);
  assert.notEqual(config.call, call);
});

test('WebSocket clone keeps header names but never submits a masked value', () => {
  const source = {
    id: 'source', name: 'Socket', type: 'websocket', interval_seconds: 60, timeout_seconds: 10,
    enabled: true, tags: [], location_ids: [], location_quorum: 1,
    config: { url: 'wss://ws.example.com/socket', headers: { Authorization: '***', 'X-Env': '***' } },
  } as unknown as Monitor;
  const clone = buildClonedMonitorInitialData(source);
  assert.deepEqual((clone.config as WebSocketMonitorConfig).headers, { Authorization: '', 'X-Env': '' });

  // Through the form: the rows come up blank (nothing stored to keep), and
  // the create request carries no "***" whatever the user leaves untouched.
  const form = initialWebsocketFormState(undefined, clone);
  assert.deepEqual(form.headers.map((row) => [row.key, row.value, Boolean(row.hasStoredValue)]), [
    ['Authorization', '', false],
    ['X-Env', '', false],
  ]);
  assert.ok(!JSON.stringify(buildWebsocketRequest(form, false)).includes(MASKED_SECRET));
  assert.equal((source.config as WebSocketMonitorConfig).headers?.Authorization, '***');
});

test('a clone of any type carries no masked secret', () => {
  // Every config the API masks (shared/secrets MonitorSecretFields and
  // MonitorSecretMapFields), served as GET /monitors/{id} returns it.
  const masked: Array<[MonitorType, Record<string, unknown>]> = [
    ['prometheus', { url: 'https://prom', query: 'up', password: '***', bearer_token: '***' }],
    ['redis', { host: 'r', password: '***', connection_string: '***', tls_client_key_pem: '***' }],
    ['postgres', { host: 'p', password: '***', connection_string: '***', tls_client_key_pem: '***' }],
    ['mongodb', { host: 'm', password: '***', connection_string: '***', tls_client_key_pem: '***' }],
    ['rabbitmq', { host: 'q', password: '***', connection_string: '***', tls_client_key_pem: '***' }],
    ['mysql', { host: 's', password: '***', connection_string: '***', tls_client_key_pem: '***' }],
    ['sip', { host: 'sbc', port: 5060, transport: 'udp', password: '***' }],
    ['push', { push_token: '***', expected_interval_seconds: 60 }],
    ['websocket', { url: 'wss://w', headers: { Authorization: '***' } }],
  ];
  for (const [type, config] of masked) {
    const source = {
      id: 'source', name: type, type, interval_seconds: 60, timeout_seconds: 10,
      enabled: true, tags: [], location_ids: [], location_quorum: 1, config,
    } as unknown as Monitor;
    const json = JSON.stringify(buildClonedMonitorInitialData(source).config);
    assert.ok(!json.includes(MASKED_SECRET), `${type} clone config ${json} carries a masked secret`);
  }
});
