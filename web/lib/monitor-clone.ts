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
      const cfg = monitor.config as SIPMonitorConfig | undefined;
      return {
        host: cfg?.host || '',
        port: cfg?.port || 5060,
        transport: cfg?.transport || 'udp',
        ...(cfg?.expected_status !== undefined ? { expected_status: cfg.expected_status } : {}),
      };
    }
    case 'prometheus': {
      const cfg = cloneObject(monitor.config as PrometheusMonitorConfig);
      if (cfg.password === MASKED_SECRET) delete cfg.password;
      if (cfg.bearer_token === MASKED_SECRET) delete cfg.bearer_token;
      return cfg;
    }
    case 'websocket': {
      const cfg = (monitor.config as WebSocketMonitorConfig | undefined) || { url: '' };
      return cloneObject(cfg);
    }
    case 'redis':
    case 'postgres':
    case 'mongodb':
    case 'rabbitmq':
    case 'mysql': {
      // Secrets come back masked from the API and can't carry over to a new
      // monitor — drop them so the clone starts with a clean credential slate.
      const cfg = cloneObject(
        (monitor.config as RedisMonitorConfig & PostgresMonitorConfig & MongoDBMonitorConfig & RabbitMQMonitorConfig & MySQLMonitorConfig | undefined) || {}
      );
      if (cfg.password === MASKED_SECRET) delete cfg.password;
      if (cfg.connection_string === MASKED_SECRET) delete cfg.connection_string;
      if (cfg.tls_client_key_pem === MASKED_SECRET) delete cfg.tls_client_key_pem;
      return cfg;
    }
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

export function buildClonedMonitorInitialData(monitor: Monitor): CreateMonitorRequest {
  const name = `${monitor.name} (Copy)`;

  return {
    name,
    type: monitor.type,
    config: buildClonedConfig(monitor),
    interval_seconds: monitor.interval_seconds || 60,
    timeout_seconds: monitor.timeout_seconds || 30,
    enabled: monitor.enabled ?? true,
    ...(monitor.tags && monitor.tags.length > 0 ? { tags: cloneObject(monitor.tags) } : {}),
    ...(monitor.depends_on_ids && monitor.depends_on_ids.length > 0
      ? { depends_on_ids: cloneObject(monitor.depends_on_ids) }
      : {}),
  };
}
