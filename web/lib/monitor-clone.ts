import {
  AgentMonitorConfig,
  CreateMonitorRequest,
  DNSMonitorConfig,
  GRPCMonitorConfig,
  GroupMonitorConfig,
  HTTPMonitorConfig,
  Monitor,
  MonitorConfig,
  PingMonitorConfig,
  PushMonitorConfig,
  SIPMonitorConfig,
  SyntheticAPIMonitorConfig,
  SyntheticBrowserMonitorConfig,
} from '@/lib/types';

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
    case 'group': {
      const cfg = monitor.config as GroupMonitorConfig | undefined;
      return {
        monitor_ids: cloneObject(cfg?.monitor_ids || monitor.member_ids || []),
      };
    }
    case 'agent': {
      const cfg = monitor.config as AgentMonitorConfig | undefined;
      return {
        agent_id: '',
        expected_interval_seconds: cfg?.expected_interval_seconds || monitor.interval_seconds || 60,
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
  };
}
