// Display names for monitor types: the one source for every operator-UI type
// label (picker, list, detail, import, incidents). This is a display mirror
// of the registry (api/internal/validation/registry.go), listed in
// CLAUDE.md's "Adding a monitor type" checklist; unknown types fall back to
// the raw id.
//
//   label  sentence-case name, used everywhere text names the type
//   badge  ≤5-char tag for the fixed-width TypeBadge (components/ui/TypeBadge.tsx),
//          close to the status-page badges in status-page/internal/statuspage/render.go
//          but naming each database rather than a generic "DB"
export const TYPE_LABELS: Record<string, { label: string; badge: string }> = {
  http: { label: 'HTTP', badge: 'HTTP' },
  ping: { label: 'Ping', badge: 'PING' },
  dns: { label: 'DNS', badge: 'DNS' },
  grpc: { label: 'gRPC', badge: 'gRPC' },
  tcp: { label: 'TCP', badge: 'TCP' },
  agent: { label: 'Agent', badge: 'AGENT' },
  group: { label: 'Group', badge: 'GROUP' },
  push: { label: 'Push', badge: 'PUSH' },
  sip: { label: 'SIP', badge: 'SIP' },
  synthetic_api: { label: 'Synthetic API', badge: 'API' },
  synthetic_browser: { label: 'Synthetic browser', badge: 'E2E' },
  redis: { label: 'Redis', badge: 'REDIS' },
  postgres: { label: 'PostgreSQL', badge: 'PG' },
  mongodb: { label: 'MongoDB', badge: 'MONGO' },
  rabbitmq: { label: 'RabbitMQ', badge: 'AMQP' },
  mysql: { label: 'MySQL', badge: 'MYSQL' },
  websocket: { label: 'WebSocket', badge: 'WS' },
  prometheus: { label: 'Prometheus', badge: 'PROM' },
  vault: { label: 'Vault', badge: 'VAULT' },
};

/**
 * Display name for a monitor type. `short` is kept for older call sites and
 * now returns the same label: one name per type everywhere. Use
 * `monitorTypeBadge` (or the TypeBadge component) for a width-bound tag.
 */
export function monitorTypeLabel(type: string, _variant: 'label' | 'short' = 'label'): string {
  return TYPE_LABELS[type]?.label ?? type.replace(/_/g, ' ');
}

/** ≤5-char uppercase tag for the TypeBadge. */
export function monitorTypeBadge(type: string): string {
  return TYPE_LABELS[type]?.badge ?? type.slice(0, 5).toUpperCase();
}
