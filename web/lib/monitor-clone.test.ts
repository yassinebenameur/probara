import assert from 'node:assert/strict';
import test from 'node:test';
import { buildClonedMonitorInitialData } from './monitor-clone';
import type { Monitor, PrometheusMonitorConfig } from './types';

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
