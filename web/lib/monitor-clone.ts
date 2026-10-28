import {
  AgentMonitorConfig,
  CreateMonitorRequest,
  DNSMonitorConfig,
  GRPCMonitorConfig,
  GroupMonitorConfig,
  HTTPMonitorConfig,
  MASKED_SECRET,
  Monitor,
  MonitorConfig,
  MongoDBMonitorConfig,
  MySQLMonitorConfig,
  PingMonitorConfig,
  PostgresMonitorConfig,
  RabbitMQMonitorConfig,
  RedisMonitorConfig,
  WebSocketMonitorConfig,
  PushMonitorConfig,
  SIPMonitorConfig,
  SyntheticAPIMonitorConfig,
  SyntheticBrowserMonitorConfig,
  TCPMonitorConfig,
  PrometheusMonitorConfig,
} from './types';

function cloneObject<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function buildClonedHTTPConfig(monitor: Monitor): HTTPMonitorConfig {
  if (monitor.config && monitor.type === 'http') {
    return cloneObject(monitor.config as HTTPMonitorConfig);
  }

  const fallback: HTTPMonitorConfig = {
    url: monitor.url || '',
    method: monitor.method || 'GET',
  };

  if (monitor.headers && Object.keys(monitor.headers).length > 0) {
    fallback.headers = { ...monitor.headers };
  }
  if (monitor.body) {
    fallback.body = monitor.body;
  }
  if (monitor.expected_status !== undefined) {
    fallback.expected_status = monitor.expected_status;
  }
  if (monitor.expected_body_substring) {
    fallback.expected_body_substring = monitor.expected_body_substring;
  }

  return fallback;
}

function buildClonedConfig(monitor: Monitor): MonitorConfig {
  switch (monitor.type) {
    case 'http':
      return buildClonedHTTPConfig(monitor);
    case 'ping': {
      const cfg = (monitor.config as PingMonitorConfig | undefined) || { host: '' };
      return cloneObject(cfg);
    }
    case 'dns': {
      const cfg = (monitor.config as DNSMonitorConfig | undefined) || { host: '', record_type: 'A' };
      return cloneObject(cfg);
    }
    case 'grpc': {
      const cfg = (monitor.config as GRPCMonitorConfig | undefined) || { host: '', port: 443, use_tls: true };
      return cloneObject(cfg);
    }
    case 'tcp': {
      const cfg = (monitor.config as TCPMonitorConfig | undefined) || { host: '', port: 0 };
      return cloneObject(cfg);
    }
    case 'group': {
      const cfg = monitor.config as GroupMonitorConfig | undefined;
      return {
        monitor_ids: cloneObject(cfg?.monitor_ids || monitor.member_ids || []),
      };
    }
    case 'agent': {
      // agent_id is cleared: the clone gets its own identity on create.
      const cfg = monitor.config as AgentMonitorConfig | undefined;
      return {
        agent_id: '',
        expected_interval_seconds: cfg?.expected_interval_seconds || monitor.interval_seconds || 60,
        ...(cfg?.metric_rules && cfg.metric_rules.length > 0
          ? { metric_rules: cloneObject(cfg.metric_rules) }
          : {}),
      };
    }
    case 'push': {
      const cfg = monitor.config as PushMonitorConfig | undefined;
      return {
        push_token: '',
        expected_interval_seconds: cfg?.expected_interval_seconds || monitor.interval_seconds || 60,
        grace_period_seconds: cfg?.grace_period_seconds ?? (monitor.interval_seconds || 60) * 2,
      };
    }
    case 'sip': {
      // Every non-secret field carries over (method, auth user, domain, TLS
      // settings and any key the form does not render). The password only
      // ever comes back masked, so the clone re-enters it.
      const fallback: SIPMonitorConfig = { host: '', port: 5060, transport: 'udp' };
      const cfg = cloneObject((monitor.config as SIPMonitorConfig | undefined) || fallback);
      delete cfg.password;
      return cfg;
    }
    case 'prometheus':
      return cloneObject(monitor.config as PrometheusMonitorConfig);
    case 'websocket': {
      const cfg = (monitor.config as WebSocketMonitorConfig | undefined) || { url: '' };
      return cloneObject(cfg);
    }
    case 'redis':
    case 'postgres':
    case 'mongodb':
    case 'rabbitmq':
    case 'mysql':
      return cloneObject(
        (monitor.config as RedisMonitorConfig & PostgresMonitorConfig & MongoDBMonitorConfig & RabbitMQMonitorConfig & MySQLMonitorConfig | undefined) || {}
      );
    case 'synthetic_api': {
      const cfg = (monitor.config as SyntheticAPIMonitorConfig | undefined) || { steps: [] };
      return cloneObject(cfg);
    }
    case 'synthetic_browser': {
      const cfg = (monitor.config as SyntheticBrowserMonitorConfig | undefined) || {
        start_url: '',
        steps: [],
      };
      return cloneObject(cfg);
    }
    default:
      return cloneObject((monitor.config || { url: '', method: 'GET' }) as MonitorConfig);
  }
}

/**
 * Removes the masked secrets a monitor read carries, for every type. Reads
 * serve each secret as "***" (shared/secrets MonitorSecretFields, scalar
 * config keys, and MonitorSecretMapFields, objects whose every value is a
 * secret), and on create there is no stored value for "***" to keep: the API
 * drops it, so a clone that kept the mask would silently lose the credential
 * while its form claimed a stored value. Rather than mirror the server's
 * per-type field list, this strips the mask wherever a secret can sit: a
 * top-level "***" string is deleted (the form shows the field empty), and a
 * "***" value inside a top-level object becomes "" so the key (a header
 * name) survives and the form asks for its value again.
 */
export function stripMaskedSecrets(config: MonitorConfig): MonitorConfig {
  const cfg = config as unknown as Record<string, unknown>;
  for (const [key, value] of Object.entries(cfg)) {
    if (value === MASKED_SECRET) {
      delete cfg[key];
    } else if (value && typeof value === 'object' && !Array.isArray(value)) {
      const values = value as Record<string, unknown>;
      for (const [inner, innerValue] of Object.entries(values)) {
        if (innerValue === MASKED_SECRET) values[inner] = '';
      }
    }
  }
  return config;
}

export function buildClonedMonitorInitialData(monitor: Monitor): CreateMonitorRequest {
  const name = `${monitor.name} (Copy)`;

  return {
    name,
    type: monitor.type,
    // buildClonedConfig returns a deep copy, so the strip never reaches the source.
    config: stripMaskedSecrets(buildClonedConfig(monitor)),
    interval_seconds: monitor.interval_seconds || 60,
    timeout_seconds: monitor.timeout_seconds || 30,
    enabled: monitor.enabled ?? true,
    ...(monitor.tags && monitor.tags.length > 0 ? { tags: cloneObject(monitor.tags) } : {}),
    ...(monitor.depends_on_ids && monitor.depends_on_ids.length > 0
      ? { depends_on_ids: cloneObject(monitor.depends_on_ids) }
      : {}),
  };
}
