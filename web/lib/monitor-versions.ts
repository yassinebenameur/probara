// Monitor configuration history (/v1/monitors/{id}/versions): the types and
// the pure helpers the monitor page's Changes tab uses; the requests live in
// lib/api/monitor-versions.ts. Every save records a masked snapshot, so a
// secret only ever reads "***" (set) or is absent, and restore never brings
// one back.

export type MonitorVersionSource = 'ui' | 'api' | 'mcp' | 'import' | 'discovery' | 'restore';
export type MonitorVersionActorType = 'user' | 'api_key' | 'system';

export interface MonitorSnapshotChannel {
  channel_id: string;
  delay_seconds: number;
}

export interface MonitorConfigSnapshot {
  name: string;
  type: string;
  config: Record<string, unknown>;
  interval_seconds: number;
  timeout_seconds: number;
  tags: string[];
  consecutive_failures_threshold: number;
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
  created_at: string;
}

export interface MonitorVersion extends MonitorVersionSummary {
  snapshot: MonitorConfigSnapshot;
}

export interface MonitorVersionListResponse {
  items: MonitorVersionSummary[];
  page: number;
  page_size: number;
  total: number;
  history_starts_at?: string;
  truncated: boolean;
  retention_limit: number;
}

export interface MonitorVersionChange {
  path: string;
  before?: unknown;
  after?: unknown;
  /** A secret field: values are only ever "***" or absent. */
  secret?: boolean;
}

export interface MonitorVersionDiff {
  version: number;
  against?: string;
  changes: MonitorVersionChange[];
}

export interface RestoreMonitorVersionResponse<M = unknown> {
  monitor: M;
  restored_from: number;
  /** Absent when the monitor already matched and nothing was written. */
  version?: number;
  dropped_references: string[];
  secrets_to_reenter: string[];
}

export const VERSION_SOURCE_LABEL: Record<MonitorVersionSource, string> = {
  ui: 'Web UI',
  api: 'API',
  mcp: 'MCP',
  import: 'Import',
  discovery: 'Discovery',
  restore: 'Restore',
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
 * One line summing up a version: what a restore re-applied, else up to `max`
 * distinct field labels (config paths collapse to "Config") then "+N more",
 * with secrets named by count only.
 */
export function summarizeVersion(
  v: Pick<MonitorVersionSummary, 'source' | 'restored_from' | 'changed_fields' | 'changed_secret_fields' | 'version'>,
  max = 3,
): string {
  if (v.source === 'restore' && v.restored_from) return `Restored version ${v.restored_from}`;
  const labels: string[] = [];
  for (const field of v.changed_fields) {
    const head = field.split('.')[0];
    const label = FIELD_LABEL[head] ?? head;
    if (!labels.includes(label)) labels.push(label);
  }
  const secrets = v.changed_secret_fields.length;
  const secretText = secrets === 1 ? '1 secret' : `${secrets} secrets`;
  if (labels.length === 0) {
    if (secrets > 0) return `${secretText} changed`;
    return v.version === 1 ? 'First recorded version' : 'No field changed';
  }
  let text = labels.length <= max ? labels.join(', ') : `${labels.slice(0, max).join(', ')} +${labels.length - max} more`;
  if (secrets > 0) text += ` · ${secretText}`;
  return text;
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
    if (value.every((item) => item !== null && typeof item === 'object' && 'channel_id' in item)) {
      return (value as MonitorSnapshotChannel[])
        .map((route) => {
          const name = names[route.channel_id] ?? route.channel_id;
          return route.delay_seconds > 0 ? `${name} after ${route.delay_seconds}s` : name;
        })
        .join(', ');
    }
  }
  return JSON.stringify(value);
}

/**
 * The two sides of a secret change as words, never values: "set", "not set",
 * and "changed" for a secret replaced by another (set on both sides).
 */
export function secretChangeText(change: MonitorVersionChange): { before: string; after: string } {
  const before = change.before !== undefined && change.before !== '';
  const after = change.after !== undefined && change.after !== '';
  return {
    before: before ? 'set' : 'not set',
    after: after ? (before ? 'changed' : 'set') : 'not set',
  };
}

/**
 * The reference a restore left out ("member:<id>") as text, with the name
 * when known.
 */
export function droppedReferenceLabel(reference: string, names: Record<string, string> = {}): string {
  const [kind, id] = reference.split(':', 2);
  const label = { channel: 'channel', location: 'location', dependency: 'dependency', member: 'member' }[kind] ?? kind;
  return `${label} ${id ? names[id] ?? id : ''}`.trim();
}
