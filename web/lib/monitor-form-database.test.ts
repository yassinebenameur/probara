import assert from 'node:assert/strict';
import test from 'node:test';
import {
  DatabaseClearedSecrets,
  DatabaseFormState,
  DatabaseMonitorType,
  buildDatabaseRequest,
  databaseSourceConfig,
  initialDatabaseFormState,
  storedDatabaseSecrets,
} from './monitor-form/database';
import { DEFAULT_ALERTING, STORED_ALERTING, assertWire, storedMonitor } from './monitor-form/testing';
import type { CreateMonitorRequest, Monitor, MonitorConfig } from './types';

// DatabaseForm (redis, postgres, mongodb, rabbitmq, mysql). password,
// connection_string and tls_client_key_pem are monitor secrets: the API
// serves "***", and on a submit absent or "***" keeps the stored value while
// "" clears it (shared/secrets MergeMonitorConfigSecrets).

const NONE_CLEARED: DatabaseClearedSecrets = { password: false, clientKey: false };
const NEW_ENVELOPE = { interval_seconds: 60, timeout_seconds: 10, enabled: true } as const;
const NEW_TAIL = { depends_on_ids: [], location_ids: [], location_quorum: 1 } as const;
const STORED_ENVELOPE = { interval_seconds: 120, timeout_seconds: 15, enabled: false } as const;
const STORED_TAIL = {
  tags: ['prod', 'edge'],
  depends_on_ids: ['dep-1'],
  location_ids: ['loc-a', 'loc-b', 'loc-c'],
  location_quorum: 2,
} as const;

/** The request a create submits for `form`. */
function create(type: DatabaseMonitorType, form: DatabaseFormState) {
  return buildDatabaseRequest(type, form, storedDatabaseSecrets(databaseSourceConfig(type), false), NONE_CLEARED);
}

/** What an edit of `monitor` submits after `edit` changes the hydrated form. */
function edit(
  type: DatabaseMonitorType,
  monitor: Monitor,
  editForm: (form: DatabaseFormState) => DatabaseFormState = (form) => form,
  cleared: DatabaseClearedSecrets = NONE_CLEARED
): CreateMonitorRequest {
  const stored = storedDatabaseSecrets(databaseSourceConfig(type, monitor), true);
  return buildDatabaseRequest(type, editForm(initialDatabaseFormState(type, monitor)), stored, cleared);
}

const DEFAULT_PORTS: Record<DatabaseMonitorType, number> = {
  redis: 6379,
  postgres: 5432,
  mongodb: 27017,
  rabbitmq: 5672,
  mysql: 3306,
};

for (const type of Object.keys(DEFAULT_PORTS) as DatabaseMonitorType[]) {
  test(`${type}: create from defaults`, () => {
    const usernameRequired = type === 'postgres' || type === 'rabbitmq' || type === 'mysql';
    const form = {
      ...initialDatabaseFormState(type),
      name: `${type} prod`,
      host: ' db.internal ',
      ...(usernameRequired ? { username: 'monitor' } : {}),
    };
    assertWire(create(type, form), {
      name: `${type} prod`,
      type,
      config: {
        host: 'db.internal',
        port: DEFAULT_PORTS[type],
        ...(usernameRequired ? { username: 'monitor' } : {}),
      },
      ...NEW_ENVELOPE,
      ...DEFAULT_ALERTING,
      ...NEW_TAIL,
    });
  });
}

// --- create, filled -------------------------------------------------------------

test('postgres: create from a filled form', () => {
  const form = {
    ...initialDatabaseFormState('postgres'),
    name: 'Orders DB',
    host: 'pg.internal',
    port: 5433,
    username: ' monitor ',
    password: 'pw',
    database: ' orders ',
    ssl_mode: 'verify-full' as const,
    tls_ca_pem: ' CA ',
    tls_client_cert_pem: ' CERT ',
    tls_client_key_pem: ' KEY ',
    query: ' select count(*) from jobs ',
    query_value_op: 'number_lt' as const,
    query_value: '100',
    max_latency_ms: '500',
    warn_latency_ms: '200',
    tags: 'db',
    location_ids: ['loc-a', 'loc-b'],
    location_quorum: 2,
  };
  assertWire(create('postgres', form), {
    name: 'Orders DB',
    type: 'postgres',
    config: {
      host: 'pg.internal',
      port: 5433,
      username: 'monitor',
      password: 'pw',
      database: 'orders',
      ssl_mode: 'verify-full',
      tls_ca_pem: 'CA',
      tls_client_cert_pem: 'CERT',
      tls_client_key_pem: 'KEY',
      query: 'select count(*) from jobs',
      query_value_op: 'number_lt',
      query_value: '100',
      max_latency_ms: 500,
      warn_latency_ms: 200,
    },
    ...NEW_ENVELOPE,
    ...DEFAULT_ALERTING,
    tags: ['db'],
    depends_on_ids: [],
    location_ids: ['loc-a', 'loc-b'],
    location_quorum: 2,
  });
});

test('mysql: create from a filled form', () => {
  const form = {
    ...initialDatabaseFormState('mysql'),
    name: 'Shop DB',
    host: 'mysql.internal',
    username: 'monitor',
    password: 'pw',
    database: 'shop',
    tls_enabled: true,
    tls_skip_verify: true,
    tls_ca_pem: 'CA',
    query: 'SELECT 1',
    query_value_op: 'equals' as const,
    query_value: '1',
  };
  assertWire(create('mysql', form).config, {
    host: 'mysql.internal',
    port: 3306,
    username: 'monitor',
    password: 'pw',
    database: 'shop',
    tls_enabled: true,
    tls_skip_verify: true,
    tls_ca_pem: 'CA',
    query: 'SELECT 1',
    query_value_op: 'equals',
    query_value: '1',
  });
});

test('redis: create from a filled form (fields and connection string)', () => {
  const fields = {
    ...initialDatabaseFormState('redis'),
    name: 'Cache',
    host: 'redis.internal',
    username: 'app',
    password: 'pw',
    db_index: 2,
    tls_enabled: true,
    expected_role: 'replica' as const,
    // Query fields only apply to postgres and mysql.
    query: 'ignored',
  };
  assertWire(create('redis', fields).config, {
    host: 'redis.internal',
    port: 6379,
    username: 'app',
    password: 'pw',
    db: 2,
    tls_enabled: true,
    expected_role: 'replica',
  });
  const viaString = {
    ...initialDatabaseFormState('redis'),
    name: 'Cache',
    connection_mode: 'connection_string' as const,
    connection_string: ' rediss://:pw@cache:6380/1 ',
    tls_skip_verify: true,
    expected_role: 'master' as const,
    max_latency_ms: '50',
  };
  assertWire(create('redis', viaString).config, {
    connection_string: 'rediss://:pw@cache:6380/1',
    tls_skip_verify: true,
    expected_role: 'master',
    max_latency_ms: 50,
  });
});

test('mongodb: create from a filled form', () => {
  const form = {
    ...initialDatabaseFormState('mongodb'),
    name: 'Mongo',
    host: 'mongo.internal',
    username: 'monitor',
    password: 'pw',
    auth_source: ' admin ',
    replica_set: ' rs0 ',
    tls_enabled: true,
    tls_client_cert_pem: 'CERT',
    tls_client_key_pem: 'KEY',
    collect_replication: true,
    collect_cpu: true,
    max_replication_lag_seconds: '30',
    warn_replication_lag_seconds: '10',
  };
  assertWire(create('mongodb', form).config, {
    host: 'mongo.internal',
    port: 27017,
    username: 'monitor',
    password: 'pw',
    auth_source: 'admin',
    replica_set: 'rs0',
    tls_enabled: true,
    tls_client_cert_pem: 'CERT',
    tls_client_key_pem: 'KEY',
    collect_replication: true,
    collect_cpu: true,
    max_replication_lag_seconds: 30,
    warn_replication_lag_seconds: 10,
  });
  // Lag thresholds only ride with the replication check.
  assertWire(create('mongodb', { ...form, collect_replication: false }).config, {
    host: 'mongo.internal',
    port: 27017,
    username: 'monitor',
    password: 'pw',
    auth_source: 'admin',
    replica_set: 'rs0',
    tls_enabled: true,
    tls_client_cert_pem: 'CERT',
    tls_client_key_pem: 'KEY',
    collect_cpu: true,
  });
});

test('rabbitmq: create from a filled form', () => {
  const form = {
    ...initialDatabaseFormState('rabbitmq'),
    name: 'Broker',
    host: 'mq.internal',
    port: 5671,
    username: 'monitor',
    password: 'pw',
    vhost: ' /prod ',
    tls_enabled: true,
    tls_skip_verify: true,
    warn_latency_ms: '100',
  };
  assertWire(create('rabbitmq', form).config, {
    host: 'mq.internal',
    port: 5671,
    username: 'monitor',
    password: 'pw',
    vhost: '/prod',
    tls_enabled: true,
    tls_skip_verify: true,
    warn_latency_ms: 100,
  });
});

// --- edit round trips ---------------------------------------------------------

test('postgres: edit round trip submits stored secrets back as "***"', () => {
  const monitor = storedMonitor('postgres', {
    host: 'pg.internal',
    port: 5432,
    username: 'monitor',
    password: '***',
    database: 'app',
    ssl_mode: 'require',
    tls_client_cert_pem: 'CERT',
    tls_client_key_pem: '***',
    query: 'select 1',
    query_value_op: 'equals',
    query_value: '1',
    max_latency_ms: 800,
    warn_latency_ms: 300,
  } as MonitorConfig);
  const form = initialDatabaseFormState('postgres', monitor);
  assert.equal(form.password, '');
  assert.equal(form.tls_client_key_pem, '');
  assertWire(edit('postgres', monitor), {
    name: 'Stored monitor',
    type: 'postgres',
    config: {
      host: 'pg.internal',
      port: 5432,
      username: 'monitor',
      password: '***',
      database: 'app',
      ssl_mode: 'require',
      tls_client_cert_pem: 'CERT',
      tls_client_key_pem: '***',
      query: 'select 1',
      query_value_op: 'equals',
      query_value: '1',
      max_latency_ms: 800,
      warn_latency_ms: 300,
    },
    ...STORED_ENVELOPE,
    ...STORED_ALERTING,
    ...STORED_TAIL,
  });
});

test('mysql: edit round trip of a connection-string monitor', () => {
  const monitor = storedMonitor('mysql', { connection_string: '***', tls_skip_verify: true, query: 'SELECT 1' } as MonitorConfig);
  const form = initialDatabaseFormState('mysql', monitor);
  assert.equal(form.connection_mode, 'connection_string');
  assertWire(edit('mysql', monitor).config, { connection_string: '***', tls_skip_verify: true, query: 'SELECT 1' });
});

test('redis: edit round trip', () => {
  const monitor = storedMonitor('redis', {
    host: 'redis.internal',
    port: 6380,
    password: '***',
    db: 3,
    tls_enabled: true,
    tls_skip_verify: true,
    expected_role: 'master',
  } as MonitorConfig);
  assertWire(edit('redis', monitor).config, {
    host: 'redis.internal',
    port: 6380,
    password: '***',
    db: 3,
    tls_enabled: true,
    tls_skip_verify: true,
    expected_role: 'master',
  });
});

test('mongodb: edit round trip', () => {
  const monitor = storedMonitor('mongodb', {
    host: 'mongo.internal',
    port: 27017,
    username: 'monitor',
    password: '***',
    auth_source: 'admin',
    replica_set: 'rs0',
    tls_enabled: true,
    tls_ca_pem: 'CA',
    collect_replication: true,
    collect_connections: true,
    collect_cache: true,
    collect_memory: true,
    collect_network: true,
    collect_cpu: true,
    max_replication_lag_seconds: 60,
    warn_replication_lag_seconds: 20,
  } as MonitorConfig);
  assertWire(edit('mongodb', monitor).config, {
    host: 'mongo.internal',
    port: 27017,
    username: 'monitor',
    password: '***',
    auth_source: 'admin',
    replica_set: 'rs0',
    tls_enabled: true,
    tls_ca_pem: 'CA',
    collect_replication: true,
    collect_connections: true,
    collect_cache: true,
    collect_memory: true,
    collect_network: true,
    collect_cpu: true,
    max_replication_lag_seconds: 60,
    warn_replication_lag_seconds: 20,
  });
});

test('rabbitmq: edit round trip', () => {
  const monitor = storedMonitor('rabbitmq', {
    host: 'mq.internal',
    port: 5672,
    username: 'monitor',
    password: '***',
    vhost: '/',
  } as MonitorConfig);
  assertWire(edit('rabbitmq', monitor).config, {
    host: 'mq.internal',
    port: 5672,
    username: 'monitor',
    password: '***',
    vhost: '/',
  });
});

// --- secret clearing ------------------------------------------------------------

const pgWithSecrets = () =>
  storedMonitor('postgres', {
    host: 'pg.internal',
    port: 5432,
    username: 'monitor',
    password: '***',
    tls_client_cert_pem: 'CERT',
    tls_client_key_pem: '***',
  } as MonitorConfig);

test('database: "Clear" on the stored password submits ""', () => {
  const config = edit('postgres', pgWithSecrets(), undefined, { password: true, clientKey: false }).config;
  assertWire(config, {
    host: 'pg.internal',
    port: 5432,
    username: 'monitor',
    password: '',
    tls_client_cert_pem: 'CERT',
    tls_client_key_pem: '***',
  });
});

test('database: a typed password replaces the stored one', () => {
  const config = edit('postgres', pgWithSecrets(), (form) => ({ ...form, password: 'new-pw' })).config;
  assert.equal((config as { password?: string }).password, 'new-pw');
});

test('database: removing the client certificate clears the stored key with ""', () => {
  const config = edit('postgres', pgWithSecrets(), (form) => ({ ...form, tls_client_cert_pem: '' }), {
    password: false,
    clientKey: true,
  }).config;
  assertWire(config, {
    host: 'pg.internal',
    port: 5432,
    username: 'monitor',
    password: '***',
    tls_client_key_pem: '',
  });
});

test('database: switching to a connection string clears the stored password with ""', () => {
  const config = edit('postgres', pgWithSecrets(), (form) => ({
    ...form,
    connection_mode: 'connection_string',
    connection_string: 'postgres://monitor:pw@pg.internal/app',
  })).config;
  assertWire(config, {
    connection_string: 'postgres://monitor:pw@pg.internal/app',
    password: '',
    tls_client_cert_pem: 'CERT',
    tls_client_key_pem: '***',
  });
});

test('database: switching from a connection string to fields clears it with ""', () => {
  const monitor = storedMonitor('redis', { connection_string: '***' } as MonitorConfig);
  const config = edit('redis', monitor, (form) => ({ ...form, connection_mode: 'fields', host: 'redis.internal' })).config;
  assertWire(config, { connection_string: '', host: 'redis.internal', port: 6379 });
});

test('database: TLS turned off clears the stored client key with ""', () => {
  // ssl_mode disable makes PEM material irrelevant, so the cert is not sent
  // (non-secret: an update replaces the config, so it drops). An omitted
  // secret is KEPT server-side, so the key has to be cleared explicitly or
  // it would outlive its certificate.
  const config = edit('postgres', pgWithSecrets(), (form) => ({ ...form, ssl_mode: 'disable' })).config;
  assertWire(config, {
    host: 'pg.internal',
    port: 5432,
    username: 'monitor',
    password: '***',
    ssl_mode: 'disable',
    tls_client_key_pem: '',
  });
});

test('database: the TLS toggle turned off clears the stored client key on every toggle type', () => {
  for (const type of ['redis', 'mongodb', 'rabbitmq', 'mysql'] as const) {
    const monitor = storedMonitor(type, {
      host: 'db.internal',
      port: DEFAULT_PORTS[type],
      username: 'monitor',
      tls_enabled: true,
      tls_client_cert_pem: 'CERT',
      tls_client_key_pem: '***',
    } as MonitorConfig);
    const kept = edit(type, monitor).config as Record<string, unknown>;
    assert.equal(kept.tls_client_key_pem, '***', `${type}: TLS on keeps the stored key`);
    const off = edit(type, monitor, (form) => ({ ...form, tls_enabled: false })).config as Record<string, unknown>;
    assert.equal(off.tls_enabled, undefined, `${type}: TLS off`);
    assert.equal(off.tls_client_cert_pem, undefined, `${type}: cert dropped`);
    assert.equal(off.tls_client_key_pem, '', `${type}: TLS off must clear the stored key with ""`);
  }
});

test('database: TLS off with no stored key sends no client key at all', () => {
  const monitor = storedMonitor('redis', { host: 'r', port: 6379, tls_enabled: true } as MonitorConfig);
  const config = edit('redis', monitor, (form) => ({ ...form, tls_enabled: false })).config;
  assert.ok(!('tls_client_key_pem' in (config as Record<string, unknown>)));
});

test('database: a clone never claims the source monitor\'s masked secrets', () => {
  const initialData: CreateMonitorRequest = {
    name: 'Copy',
    type: 'postgres',
    config: { host: 'pg.internal', port: 5432, username: 'monitor', password: '***' } as MonitorConfig,
    interval_seconds: 60,
    timeout_seconds: 10,
  };
  assertWire(storedDatabaseSecrets(databaseSourceConfig('postgres', undefined, initialData), false), {
    password: false,
    connectionString: false,
    clientKey: false,
  });
  const form = initialDatabaseFormState('postgres', undefined, initialData);
  assertWire(create('postgres', form).config, { host: 'pg.internal', port: 5432, username: 'monitor' });
});
