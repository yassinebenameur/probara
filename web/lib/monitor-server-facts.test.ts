import assert from 'node:assert/strict';
import test from 'node:test';
import { formatDate } from './format';
import { serverFacts } from './monitor-server-facts';
import type { CheckResult, Monitor } from './types';

function monitor(type: string, config: unknown = {}): Monitor {
  return { id: 'm1', name: 'm1', type, config } as unknown as Monitor;
}

function result(id: string, metrics: unknown): CheckResult {
  return { id, status: 'success', metrics_data: metrics, created_at: '2026-05-21T00:00:00.000Z' } as unknown as CheckResult;
}

test('serverFacts: nothing for types without a Server panel, or without metrics yet', () => {
  assert.deepEqual(serverFacts(monitor('http'), [result('a', { http: {} })]), []);
  assert.deepEqual(serverFacts(monitor('postgres'), []), []);
  assert.deepEqual(serverFacts(monitor('postgres'), [result('a', {})]), []);
  // Another type's envelope is not this monitor's.
  assert.deepEqual(serverFacts(monitor('postgres'), [result('a', { redis: { product: 'Redis' } })]), []);
});

test('serverFacts: a database reports product, version, role and replica set on one Version row', () => {
  const facts = serverFacts(monitor('postgres'), [
    result('a', { postgres: { product: 'PostgreSQL', server_version: '16.2', role: 'primary' } }),
  ]);
  assert.deepEqual(facts, [{ key: 'server', label: 'Version', value: 'PostgreSQL 16.2, primary' }]);

  const withSet = serverFacts(monitor('redis'), [result('a', { redis: { server_version: '7.2', replica_set: 'rs0' } })]);
  assert.deepEqual(withSet, [{ key: 'server', label: 'Version', value: '7.2, set rs0' }]);
});

test('serverFacts: reads the newest result that carries the type metrics', () => {
  const facts = serverFacts(monitor('mysql'), [
    result('newest', {}),
    result('older', { mysql: { product: 'MySQL', server_version: '8.4' } }),
    result('oldest', { mysql: { product: 'MySQL', server_version: '8.0' } }),
  ]);
  assert.deepEqual(facts, [{ key: 'server', label: 'Version', value: 'MySQL 8.4' }]);
});

test('serverFacts: a latency warning follows the version', () => {
  const facts = serverFacts(monitor('rabbitmq'), [
    result('a', { rabbitmq: { product: 'RabbitMQ', latency_warn_ms: 250 } }),
  ]);
  assert.deepEqual(facts, [
    { key: 'server', label: 'Version', value: 'RabbitMQ' },
    { key: 'latency-warn', label: 'Warning', tone: 'warning', value: 'latency over 250ms threshold' },
  ]);
});

test('serverFacts: mongodb adds cluster rows, then warnings, then notes', () => {
  const facts = serverFacts(monitor('mongodb'), [
    result('a', {
      mongodb: {
        product: 'MongoDB',
        server_version: '7.0',
        connected_clients: 12,
        connections_available: 800,
        network_bytes_in: 0,
        network_bytes_out: 0,
        replication: { members_healthy: 2, members_total: 3, max_lag_seconds: 1.25, primary: '' },
        replication_lag_warn_seconds: 10,
        unavailable: [{ reason: 'unauthorized' }, { reason: 'not_replica_set' }, { reason: 'timeout' }],
      },
    }),
  ]);
  assert.deepEqual(
    facts.map((fact) => [fact.label, fact.tone ?? '', fact.value]),
    [
      ['Version', '', 'MongoDB 7.0'],
      ['Connections', '', '12 current, 800 available'],
      ['Network', '', `${facts[2].value}`],
      ['Replication', '', '2/3 healthy, lag 1.3s'],
      ['Warning', 'warning', 'replication lag over 10s threshold'],
      ['Warning', 'warning', 'replica set has no primary'],
      ['Warning', 'warning', 'unhealthy replica set member(s)'],
      ['Note', 'note', 'Cluster checks skipped: grant clusterMonitor'],
      ['Note', 'note', 'Replication: not a replica set'],
      ['Note', 'note', 'Cluster checks temporarily unavailable'],
    ]
  );
  assert.match(facts[2].value, / in, .* out$/);
  // Warning and note rows key by their text, cluster rows by their label.
  assert.equal(facts[1].key, 'Connections');
  assert.equal(facts[5].key, 'replica set has no primary');
});

test('serverFacts: mongodb rows need both halves of a pair; a healthy set names its primary', () => {
  const facts = serverFacts(monitor('mongodb'), [
    result('a', {
      mongodb: {
        connected_clients: 12,
        cache_used_bytes: 1,
        replication: { members_healthy: 3, members_total: 3, primary: 'db1:27017' },
      },
    }),
  ]);
  assert.deepEqual(facts, [
    { key: 'Replication', label: 'Replication', value: '3/3 healthy, primary db1:27017' },
  ]);
});

test('serverFacts: tcp shows TLS only when the monitor uses it', () => {
  assert.deepEqual(serverFacts(monitor('tcp', { use_tls: false }), [result('a', { tcp: { tls_version: 'TLS 1.3' } })]), []);
  assert.deepEqual(serverFacts(monitor('tcp', { use_tls: true }), [result('a', { tcp: { tls_version: 'TLS 1.3' } })]), [
    { key: 'tls', label: 'TLS', value: 'TLS 1.3' },
  ]);
  assert.deepEqual(serverFacts(monitor('tcp', { use_tls: true }), []), [{ key: 'tls', label: 'TLS', value: 'Enabled' }]);
});

test('serverFacts: the certificate row names an intermediate that expires first, as the alert does', () => {
  const tls = {
    subject: 'CN=imap.example.com',
    not_after: '2027-06-01T00:00:00Z',
    days_until_expiry: 200,
    chain_not_after: '2026-11-25T00:00:00Z',
    chain_min_days: 9,
    chain_expiring_subject: 'CN=Example CA 2',
  };
  const tcp = serverFacts(monitor('tcp', { host: 'imap.example.com', port: 993, use_tls: true, tls_min_days_valid: 14 }), [
    result('a', { tcp: { use_tls: true, tls_version: 'TLS 1.3', tls } }),
  ]);
  assert.deepEqual(tcp, [
    { key: 'tls', label: 'TLS', value: 'TLS 1.3' },
    {
      key: 'certificate',
      label: 'Certificate',
      value: `intermediate CN=Example CA 2 expires in 9 days (${formatDate('2026-11-25T00:00:00Z')})`,
      tone: 'warning',
    },
  ]);

  // The leaf, recorded before chains were measured, outside the window.
  const http = serverFacts(monitor('http', { url: 'https://a.example', method: 'GET', tls_min_days_valid: 14 }), [
    result('a', { http: {} }),
    result('b', { http: { tls: { subject: 'CN=a.example', not_after: '2027-06-01T00:00:00Z', days_until_expiry: 200 } } }),
  ]);
  assert.deepEqual(http, [
    { key: 'certificate', label: 'Certificate', value: `expires in 200 days (${formatDate('2027-06-01T00:00:00Z')})`, tone: undefined },
  ]);
});

test('serverFacts: a ping reports loss, round trip and a shortened echo gap', () => {
  const facts = serverFacts(monitor('ping', { host: 'gw', interval_ms: 1000, max_rtt_ms: 5 }), [
    result('a', { ping: { sent: 3, received: 2, loss_pct: 33.33, interval_ms: 250, rtt_min_ms: 0.42, rtt_avg_ms: 6.25, rtt_max_ms: 12.1 } }),
  ]);
  assert.deepEqual(facts, [
    { key: 'loss', label: 'Packet loss', value: '33.33% (2/3 echoes answered)', tone: undefined },
    { key: 'rtt', label: 'Round trip', value: 'min 0.4 ms · avg 6.3 ms · max 12 ms', tone: 'warning' },
    { key: 'echo-gap', label: 'Echo gap', value: '250 ms, shortened from 1000 ms to fit the timeout', tone: 'note' },
  ]);

  const lost = serverFacts(monitor('ping', { host: 'gw' }), [result('a', { ping: { sent: 3, received: 0, loss_pct: 100, interval_ms: 1000 } })]);
  assert.deepEqual(lost, [{ key: 'loss', label: 'Packet loss', value: '100.00% (0/3 echoes answered)', tone: 'warning' }]);
});
