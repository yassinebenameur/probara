import assert from 'node:assert/strict';
import test from 'node:test';
import { buildClonedMonitorInitialData } from './monitor-clone';
import type { Monitor, PrometheusMonitorConfig, VaultMonitorConfig } from './types';

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

test('Vault clone keeps address and rules but drops the masked token and client key', () => {
  const source = {
    id: 'source', name: 'Vault', type: 'vault', interval_seconds: 60, timeout_seconds: 10,
    enabled: true, tags: ['production'], location_ids: ['location'], location_quorum: 1,
    config: { url: 'https://vault.example.com:8200', token: '***', tls_client_key_pem: '***',
      tls_client_cert_pem: 'cert', require_active: true, token_min_ttl_seconds: 3600 },
  } as Monitor;
  const clone = buildClonedMonitorInitialData(source);
  const config = clone.config as VaultMonitorConfig;
  assert.equal(clone.type, 'vault');
  assert.equal(config.url, 'https://vault.example.com:8200');
  assert.equal(config.require_active, true);
  assert.equal(config.token_min_ttl_seconds, 3600);
  assert.equal(config.token, undefined);
  assert.equal(config.tls_client_key_pem, undefined);
  assert.equal((source.config as VaultMonitorConfig).token, '***');
});
