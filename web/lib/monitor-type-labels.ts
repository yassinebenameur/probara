// Display names for monitor types. This is a display mirror of the registry
// (api/internal/validation/registry.go), listed in CLAUDE.md's "Adding a
// monitor type" checklist; unknown types fall back to the raw id.
export const TYPE_LABELS: Record<string, { label: string; short: string }> = {
  http: { label: 'HTTP', short: 'HTTP' },
  ping: { label: 'Ping', short: 'Ping' },
  dns: { label: 'DNS', short: 'DNS' },
  grpc: { label: 'gRPC', short: 'gRPC' },
  tcp: { label: 'TCP', short: 'TCP' },
  agent: { label: 'Agent', short: 'Agent' },
  group: { label: 'Group', short: 'Group' },
  push: { label: 'Push', short: 'Push' },
  sip: { label: 'SIP', short: 'SIP' },
  synthetic_api: { label: 'Synthetic API', short: 'Synthetic API' },
  synthetic_browser: { label: 'Synthetic browser', short: 'Synthetic browser' },
  redis: { label: 'Redis', short: 'Redis' },
  postgres: { label: 'PostgreSQL', short: 'Postgres' },
  mongodb: { label: 'MongoDB', short: 'MongoDB' },
  rabbitmq: { label: 'RabbitMQ', short: 'RabbitMQ' },
  mysql: { label: 'MySQL', short: 'MySQL' },
  websocket: { label: 'WebSocket', short: 'WebSocket' },
  prometheus: { label: 'Prometheus', short: 'PromQL' },
};

export function monitorTypeLabel(type: string, variant: 'label' | 'short' = 'label'): string {
  return TYPE_LABELS[type]?.[variant] ?? type.replace(/_/g, ' ');
}
