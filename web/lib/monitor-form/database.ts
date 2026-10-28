import {
  CreateMonitorRequest,
  DBQueryValueOp,
  MASKED_SECRET,
  Monitor,
  MonitorType,
  MongoDBMonitorConfig,
  MySQLMonitorConfig,
  PostgresMonitorConfig,
  RabbitMQMonitorConfig,
  RedisMonitorConfig,
} from '../types';
import { buildScheduledMonitorRequest, initialScheduledFormFields } from './common';

// DatabaseForm's per-type metadata, state and request (redis, postgres,
// mongodb, rabbitmq, mysql). password, connection_string and
// tls_client_key_pem are monitor secrets: the API returns them as "***" and
// merges a submit per field — absent or "***" keeps the stored value, ""
// clears it — so every deliberate drop must submit "".

export type DatabaseMonitorType = 'redis' | 'postgres' | 'mongodb' | 'rabbitmq' | 'mysql';

export type DatabaseConfig = RedisMonitorConfig &
  PostgresMonitorConfig &
  MongoDBMonitorConfig &
  RabbitMQMonitorConfig &
  MySQLMonitorConfig;

// Types whose checker supports the optional assertion query + value assertion.
export const QUERY_CAPABLE_TYPES: DatabaseMonitorType[] = ['postgres', 'mysql'];

export type ConnectionMode = 'fields' | 'connection_string';

export const DB_TYPE_META: Record<
  DatabaseMonitorType,
  {
    label: string;
    defaultPort: number;
    hostPlaceholder: string;
    csPlaceholder: string;
    csHint: string;
    csSchemes: string[];
    csAllowsKeyValueDSN: boolean;
    // Accepts the driver's native non-URI DSN (mysql: user:pw@tcp(host)/db).
    csAllowsNativeDSN?: boolean;
    supportsTLSToggle: boolean;
    usernameRequired: boolean;
    checkVerb: string;
  }
> = {
  redis: {
    label: 'Redis',
    defaultPort: 6379,
    hostPlaceholder: 'redis.internal',
    csPlaceholder: 'redis://user:password@redis.internal:6379/0',
    csHint: 'redis:// or rediss:// (TLS) URI. May include username, password, and DB index.',
    csSchemes: ['redis://', 'rediss://'],
    csAllowsKeyValueDSN: false,
    supportsTLSToggle: true,
    usernameRequired: false,
    checkVerb: 'connect + PING',
  },
  postgres: {
    label: 'PostgreSQL',
    defaultPort: 5432,
    hostPlaceholder: 'db.internal',
    csPlaceholder: 'postgres://user:password@db.internal:5432/mydb?sslmode=require',
    csHint: 'postgres:// URI or key=value DSN. TLS is controlled by sslmode in the string.',
    csSchemes: ['postgres://', 'postgresql://'],
    csAllowsKeyValueDSN: true,
    supportsTLSToggle: false,
    usernameRequired: true,
    checkVerb: 'connect + ping',
  },
  mongodb: {
    label: 'MongoDB',
    defaultPort: 27017,
    hostPlaceholder: 'mongo.internal',
    csPlaceholder: 'mongodb://user:password@mongo.internal:27017/?authSource=admin',
    csHint: 'mongodb:// or mongodb+srv:// URI. May include credentials and options like tls=true.',
    csSchemes: ['mongodb://', 'mongodb+srv://'],
    csAllowsKeyValueDSN: false,
    supportsTLSToggle: true,
    usernameRequired: false,
    checkVerb: 'connect + ping',
  },
  rabbitmq: {
    label: 'RabbitMQ',
    defaultPort: 5672,
    hostPlaceholder: 'mq.internal',
    csPlaceholder: 'amqp://user:password@mq.internal:5672/vhost',
    csHint: 'amqp:// or amqps:// (TLS) URI. May include credentials and the vhost path.',
    csSchemes: ['amqp://', 'amqps://'],
    csAllowsKeyValueDSN: false,
    supportsTLSToggle: true,
    usernameRequired: true,
    checkVerb: 'AMQP handshake',
  },
  mysql: {
    label: 'MySQL',
    defaultPort: 3306,
    hostPlaceholder: 'db.internal',
    csPlaceholder: 'mysql://user:password@db.internal:3306/mydb',
    csHint: 'mysql:// URI or a driver DSN like user:password@tcp(db.internal:3306)/mydb.',
    csSchemes: ['mysql://'],
    csAllowsKeyValueDSN: false,
    csAllowsNativeDSN: true,
    supportsTLSToggle: true,
    usernameRequired: true,
    checkVerb: 'connect + ping',
  },
};

/** The masked secrets the edited monitor holds (a clone never inherits them). */
export interface DatabaseStoredSecrets {
  password: boolean;
  connectionString: boolean;
  clientKey: boolean;
}

/** Stored secrets the operator dropped with a Clear affordance. */
export interface DatabaseClearedSecrets {
  password: boolean;
  clientKey: boolean;
}

/** The config DatabaseForm hydrates from: the stored monitor's, else the clone's. */
export function databaseSourceConfig(
  type: DatabaseMonitorType,
  monitor?: Monitor,
  initialData?: CreateMonitorRequest
): DatabaseConfig {
  const isEditMode = Boolean(monitor);
  return monitor && monitor.type === type
    ? ((monitor.config || {}) as DatabaseConfig)
    : !isEditMode && initialData?.type === type
      ? ((initialData.config || {}) as DatabaseConfig)
      : ({} as DatabaseConfig);
}

// The API masks stored secrets as "***" — that tells us one exists without
// revealing it. Cloned monitors carry the mask too, but a clone can't reuse
// another monitor's secret, so treat it as unset there.
export function storedDatabaseSecrets(existingConfig: DatabaseConfig, isEditMode: boolean): DatabaseStoredSecrets {
  return {
    password: isEditMode && existingConfig.password === MASKED_SECRET,
    connectionString: isEditMode && existingConfig.connection_string === MASKED_SECRET,
    clientKey: isEditMode && existingConfig.tls_client_key_pem === MASKED_SECRET,
  };
}

/**
 * DatabaseForm's initial state. Secret inputs always start blank: the stored
 * values are write-only, so `storedDatabaseSecrets` says which exist.
 */
export function initialDatabaseFormState(
  type: DatabaseMonitorType,
  monitor?: Monitor,
  initialData?: CreateMonitorRequest
) {
  const meta = DB_TYPE_META[type];
  const existingConfig = databaseSourceConfig(type, monitor, initialData);
  return {
    ...initialScheduledFormFields(monitor, initialData),
    connection_mode: (existingConfig.connection_string ? 'connection_string' : 'fields') as ConnectionMode,
    connection_string: '',
    host: existingConfig.host || '',
    port: existingConfig.port || meta.defaultPort,
    username: existingConfig.username || '',
    password: '',
    database: existingConfig.database || '',
    ssl_mode: existingConfig.ssl_mode || ('' as '' | 'disable' | 'require' | 'verify-full'),
    auth_source: existingConfig.auth_source || '',
    replica_set: existingConfig.replica_set || '',
    vhost: existingConfig.vhost || '',
    db_index: existingConfig.db ?? 0,
    tls_enabled: existingConfig.tls_enabled ?? false,
    tls_skip_verify: existingConfig.tls_skip_verify ?? false,
    tls_ca_pem: existingConfig.tls_ca_pem || '',
    tls_client_cert_pem: existingConfig.tls_client_cert_pem || '',
    tls_client_key_pem: '',
    query: existingConfig.query || '',
    query_value_op: existingConfig.query_value_op || ('' as '' | DBQueryValueOp),
    query_value: existingConfig.query_value || '',
    expected_role: existingConfig.expected_role || ('' as '' | 'master' | 'replica'),
    max_latency_ms: existingConfig.max_latency_ms ? String(existingConfig.max_latency_ms) : '',
    warn_latency_ms: existingConfig.warn_latency_ms ? String(existingConfig.warn_latency_ms) : '',
    collect_replication: existingConfig.collect_replication ?? false,
    collect_connections: existingConfig.collect_connections ?? false,
    collect_cache: existingConfig.collect_cache ?? false,
    collect_memory: existingConfig.collect_memory ?? false,
    collect_network: existingConfig.collect_network ?? false,
    collect_cpu: existingConfig.collect_cpu ?? false,
    max_replication_lag_seconds: existingConfig.max_replication_lag_seconds
      ? String(existingConfig.max_replication_lag_seconds)
      : '',
    warn_replication_lag_seconds: existingConfig.warn_replication_lag_seconds
      ? String(existingConfig.warn_replication_lag_seconds)
      : '',
  };
}

export type DatabaseFormState = ReturnType<typeof initialDatabaseFormState>;

/** Whether PEM material applies: the connection can use TLS. */
export function databaseTLSMaterialRelevant(type: DatabaseMonitorType, form: DatabaseFormState): boolean {
  const usingConnString = form.connection_mode === 'connection_string';
  return usingConnString
    ? true
    : type === 'postgres'
      ? form.ssl_mode !== 'disable'
      : form.tls_enabled;
}

/** The config DatabaseForm submits and tests, for a form that passed validation. */
export function buildDatabaseConfig(
  type: DatabaseMonitorType,
  form: DatabaseFormState,
  stored: DatabaseStoredSecrets,
  cleared: DatabaseClearedSecrets
): DatabaseConfig {
  const meta = DB_TYPE_META[type];
  const usingConnString = form.connection_mode === 'connection_string';
  const tlsMaterialRelevant = databaseTLSMaterialRelevant(type, form);
  const storedPassword = stored.password;
  const storedConnString = stored.connectionString;
  const storedClientKey = stored.clientKey;
  const hasStoredPassword = storedPassword && !cleared.password;

  const config: DatabaseConfig = {};

  // An omitted secret field means "keep the stored value" server-side, so
  // every deliberate drop — the Clear affordances and the connection-mode
  // switch — has to submit "" (the explicit clear) instead of leaving the
  // field out.
  if (usingConnString) {
    // Blank + previously stored = keep the stored secret (write-only).
    config.connection_string = form.connection_string.trim() || MASKED_SECRET;
    if (storedPassword) config.password = '';
    if (meta.supportsTLSToggle && form.tls_skip_verify) config.tls_skip_verify = true;
  } else {
    if (storedConnString) config.connection_string = '';
    config.host = form.host.trim();
    config.port = form.port;
    if (form.username.trim()) config.username = form.username.trim();
    if (form.password) {
      config.password = form.password;
    } else if (hasStoredPassword) {
      config.password = MASKED_SECRET;
    } else if (storedPassword) {
      // "Clear" on the set-chip.
      config.password = '';
    }
    if (type === 'redis' && form.db_index > 0) config.db = form.db_index;
    if (type === 'postgres') {
      if (form.database.trim()) config.database = form.database.trim();
      if (form.ssl_mode) config.ssl_mode = form.ssl_mode;
    }
    if (type === 'mongodb') {
      if (form.auth_source.trim()) config.auth_source = form.auth_source.trim();
      if (form.replica_set.trim()) config.replica_set = form.replica_set.trim();
    }
    if (type === 'rabbitmq' && form.vhost.trim()) config.vhost = form.vhost.trim();
    if (type === 'mysql' && form.database.trim()) config.database = form.database.trim();
    if (meta.supportsTLSToggle && form.tls_enabled) {
      config.tls_enabled = true;
      if (form.tls_skip_verify) config.tls_skip_verify = true;
    }
  }

  if (tlsMaterialRelevant) {
    if (form.tls_ca_pem.trim()) config.tls_ca_pem = form.tls_ca_pem.trim();
    if (form.tls_client_cert_pem.trim()) {
      config.tls_client_cert_pem = form.tls_client_cert_pem.trim();
      // Blank + previously stored = keep the stored key (write-only).
      config.tls_client_key_pem = form.tls_client_key_pem.trim() || MASKED_SECRET;
    } else if (storedClientKey) {
      // Client cert removed (or the stored key cleared): "" is the explicit
      // clear — omitting the field would keep the stored key.
      config.tls_client_key_pem = '';
    }
  } else if (storedClientKey) {
    // TLS turned off (the toggle, or Postgres sslmode=disable): the CA and
    // cert are not sent, so they drop, but an omitted secret is kept — clear
    // the key with "" or it outlives its certificate.
    config.tls_client_key_pem = '';
  }

  if (type === 'redis' && form.expected_role) config.expected_role = form.expected_role;
  if (type === 'mongodb') {
    // Cluster checks apply in both connection modes — post-connect commands.
    if (form.collect_replication) config.collect_replication = true;
    if (form.collect_connections) config.collect_connections = true;
    if (form.collect_cache) config.collect_cache = true;
    if (form.collect_memory) config.collect_memory = true;
    if (form.collect_network) config.collect_network = true;
    if (form.collect_cpu) config.collect_cpu = true;
    if (form.collect_replication) {
      const maxLag = form.max_replication_lag_seconds.trim()
        ? parseInt(form.max_replication_lag_seconds, 10)
        : undefined;
      const warnLag = form.warn_replication_lag_seconds.trim()
        ? parseInt(form.warn_replication_lag_seconds, 10)
        : undefined;
      if (maxLag) config.max_replication_lag_seconds = maxLag;
      if (warnLag) config.warn_replication_lag_seconds = warnLag;
    }
  }
  if (QUERY_CAPABLE_TYPES.includes(type) && form.query.trim()) {
    config.query = form.query.trim();
    if (form.query_value_op) {
      config.query_value_op = form.query_value_op;
      config.query_value = form.query_value;
    }
  }
  const maxLatency = form.max_latency_ms.trim() ? parseInt(form.max_latency_ms, 10) : undefined;
  const warnLatency = form.warn_latency_ms.trim() ? parseInt(form.warn_latency_ms, 10) : undefined;
  if (maxLatency) config.max_latency_ms = maxLatency;
  if (warnLatency) config.warn_latency_ms = warnLatency;

  return config;
}

/** The request DatabaseForm submits. */
export function buildDatabaseRequest(
  type: DatabaseMonitorType,
  form: DatabaseFormState,
  stored: DatabaseStoredSecrets,
  cleared: DatabaseClearedSecrets
): CreateMonitorRequest {
  return buildScheduledMonitorRequest(type as MonitorType, buildDatabaseConfig(type, form, stored, cleared), form);
}
