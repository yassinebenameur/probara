// Monitor config version history (/v1/monitors/{id}/versions): types and the
// pure helpers the monitor's Config history tab uses; the requests live in
// lib/api.ts. Every create and update records a masked snapshot, so secrets
// read "***" and are never restored.

export type MonitorVersionSource = 'api' | 'ui' | 'mcp' | 'import' | 'apply' | 'restore' | 'discovery';
export type MonitorVersionActorType = 'user' | 'api_key' | 'system';

export interface MonitorSnapshotChannel {
  channel_id: string;
  delay_seconds: number;
  notify_after_ack: boolean;
}

export interface MonitorConfigSnapshot {
  name: string;
  type: string;
  config: Record<string, unknown>;
  interval_seconds: number;
  timeout_seconds: number;
  tags: string[];
  consecutive_failures_threshold: number;
  recovery_threshold: number;
  notification_mode: string;
  notification_channels: MonitorSnapshotChannel[];
  member_alert_rollup: string;
  dependency_suppression: string;
  location_ids: string[];
  location_quorum: number;
  depends_on_ids: string[];
  member_ids: string[];
}

export interface MonitorVersionSummary {
  version: number;
  source: MonitorVersionSource;
  actor_type: MonitorVersionActorType;
  actor_id?: string;
  actor_label: string;
  restored_from?: number;
  changed_fields: string[];
  changed_secret_fields: string[];
  snapshot_hash: string;
  created_at: string;
}

export interface MonitorVersion extends MonitorVersionSummary {
  snapshot: MonitorConfigSnapshot;
}

export interface MonitorVersionListResponse {
  items: MonitorVersionSummary[];
  history_starts_at?: string;
  truncated: boolean;
  retention_limit: number;
}

export interface MonitorVersionChange {
  path: string;
  before?: unknown;
  after?: unknown;
}

export interface MonitorVersionDiff {
  version: number;
  against?: string;
  changes: MonitorVersionChange[];
}

export interface RestoreMonitorVersionResponse<M = unknown> {
  monitor: M;
  restored_from: number;
  dropped_references: string[];
}

export const VERSION_SOURCE_LABEL: Record<MonitorVersionSource, string> = {
  api: 'API',
  ui: 'Web UI',
  mcp: 'MCP',
  import: 'Import',
  apply: 'Apply',
  restore: 'Restore',
  discovery: 'Discovery',
};

/** Who made a change: the username or key name, else the credential kind. */
export function versionActorLabel(v: Pick<MonitorVersionSummary, 'actor_type' | 'actor_label'>): string {
  if (v.actor_label) return v.actor_label;
  if (v.actor_type === 'api_key') return 'API key';
  if (v.actor_type === 'user') return 'Deleted user';
  return 'System';
}

const FIELD_LABEL: Record<string, string> = {
  name: 'Name',
  type: 'Type',
  config: 'Config',
  interval_seconds: 'Interval',
  timeout_seconds: 'Timeout',
  tags: 'Tags',
  consecutive_failures_threshold: 'Failure threshold',
  recovery_threshold: 'Recovery threshold',
  notification_mode: 'Alert routing',
  notification_channels: 'Channels',
  member_alert_rollup: 'Member alerts',
  dependency_suppression: 'Dependency suppression',
  location_ids: 'Locations',
  location_quorum: 'Location quorum',
  depends_on_ids: 'Dependencies',
  member_ids: 'Members',
};

/** A readable name for a snapshot path: "Config › url", "Locations". */
export function versionFieldLabel(path: string): string {
  const [head, ...rest] = path.split('.');
  const label = FIELD_LABEL[head] ?? head;
  return rest.length > 0 ? `${label} › ${rest.join('.')}` : label;
}

/**
 * One line summing up what a version changed: up to `max` distinct field
 * labels (config paths collapse to "Config"), then "+N more".
 */
export function summarizeChangedFields(fields: string[], max = 3): string {
  const labels: string[] = [];
  for (const field of fields) {
    const label = FIELD_LABEL[field.split('.')[0]] ?? field.split('.')[0];
    if (!labels.includes(label)) labels.push(label);
  }
  if (labels.length <= max) return labels.join(', ');
  return `${labels.slice(0, max).join(', ')} +${labels.length - max} more`;
}

/**
 * A diff value as text. IDs are swapped for names through `names` when
 * known; objects and arrays fall back to compact JSON.
 */
export function formatVersionValue(value: unknown, names: Record<string, string> = {}): string {
  if (value === undefined) return '';
  if (value === null) return 'null';
  if (typeof value === 'string') return names[value] ?? value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) {
    if (value.length === 0) return '(none)';
    if (value.every((item) => typeof item === 'string')) {
      return (value as string[]).map((item) => names[item] ?? item).join(', ');
    }
    if (value.every((item) => item && typeof item === 'object' && 'channel_id' in item)) {
      return (value as MonitorSnapshotChannel[])
        .map((route) => {
          const name = names[route.channel_id] ?? route.channel_id;
          const delay = route.delay_seconds > 0 ? ` after ${route.delay_seconds}s` : '';
          return `${name}${delay}${route.notify_after_ack ? ' (pages after ack)' : ''}`;
        })
        .join(', ');
    }
  }
  return JSON.stringify(value);
}
