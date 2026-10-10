// Display rules for the generic metric store (single source for the UI).
// Mirrors the curated label map in Go shared/metricstore/meta.go — keep the
// two in sync when adding curated metrics.

export type MetricUnitKind = 'ratio' | 'bytes' | 'seconds' | 'count';

export interface MetricMeta {
  label: string;
  short: string;
  unit: MetricUnitKind;
  // Legacy host_metric alert kinds ('cpu'|'memory'|'disk'|'swap'): their
  // alert values are already percent 0-100, not ratios.
  legacyPercent?: boolean;
}

export const METRIC_META: Record<string, MetricMeta> = {
  'system.cpu.utilization': { label: 'CPU usage', short: 'CPU', unit: 'ratio' },
  'system.memory.utilization': { label: 'Memory usage', short: 'Memory', unit: 'ratio' },
  'system.memory.usage': { label: 'Memory used', short: 'Memory', unit: 'bytes' },
  'system.paging.utilization': { label: 'Swap usage', short: 'Swap', unit: 'ratio' },
  'system.paging.usage': { label: 'Swap used', short: 'Swap', unit: 'bytes' },
  'system.filesystem.utilization': { label: 'Filesystem usage', short: 'Filesystem', unit: 'ratio' },
  'system.filesystem.usage': { label: 'Filesystem used', short: 'Filesystem', unit: 'bytes' },
  'system.cpu.load_average.1m': { label: 'Load 1m', short: 'Load 1m', unit: 'count' },
  'system.cpu.load_average.5m': { label: 'Load 5m', short: 'Load 5m', unit: 'count' },
  'system.cpu.load_average.15m': { label: 'Load 15m', short: 'Load 15m', unit: 'count' },
  'system.network.io': { label: 'Network I/O', short: 'Network', unit: 'bytes' },
  'system.disk.io': { label: 'Disk I/O', short: 'Disk I/O', unit: 'bytes' },
  'system.uptime': { label: 'Uptime', short: 'Uptime', unit: 'seconds' },
  'system.processes.count': { label: 'Processes', short: 'Processes', unit: 'count' },
  'system.cpu.logical.count': { label: 'CPU cores', short: 'Cores', unit: 'count' },
  // Legacy host_metric alert kinds from pre-OTel agents.
  cpu: { label: 'CPU usage', short: 'CPU', unit: 'ratio', legacyPercent: true },
  memory: { label: 'Memory usage', short: 'Memory', unit: 'ratio', legacyPercent: true },
  disk: { label: 'Disk usage', short: 'Disk', unit: 'ratio', legacyPercent: true },
  swap: { label: 'Swap usage', short: 'Swap', unit: 'ratio', legacyPercent: true },
};

export interface ParsedSeriesKey {
  name: string;
  attributes: Record<string, string>;
}

// Parses a canonical series key like
// "system.filesystem.utilization{mountpoint=/data,state=used}".
// Attribute keys/values may not contain ',', '}' or '=' (server-enforced),
// so a plain split is exact. A bare name ("cpu") parses to empty attributes.
export function parseSeriesKey(key: string): ParsedSeriesKey {
  const brace = key.indexOf('{');
  if (brace === -1) {
    return { name: key, attributes: {} };
  }
  const name = key.slice(0, brace);
  const inner = key.slice(brace + 1, key.endsWith('}') ? key.length - 1 : key.length);
  const attributes: Record<string, string> = {};
  for (const pair of inner.split(',')) {
    if (!pair) continue;
    const eq = pair.indexOf('=');
    if (eq === -1) continue;
    attributes[pair.slice(0, eq)] = pair.slice(eq + 1);
  }
  return { name, attributes };
}

// Ratio (0-1) ↔ percent (0-100) helpers for *.utilization thresholds.
export function ratioToPercent(ratio: number): number {
  return Math.round(ratio * 100 * 1000) / 1000;
}

export function percentToRatio(percent: number): number {
  return Math.round((percent / 100) * 100000) / 100000;
}

// Server rule: *.utilization thresholds are ratios and must be ≤ 1.
export function isUtilizationMetric(metricName: string): boolean {
  return metricName.endsWith('.utilization');
}

// Unit kind for a metric: curated map first, then the OTLP unit string.
export function metricUnitKind(metricName: string, unit?: string): MetricUnitKind {
  const meta = METRIC_META[metricName];
  if (meta) return meta.unit;
  if (isUtilizationMetric(metricName)) return 'ratio';
  switch (unit) {
    case 'By':
      return 'bytes';
    case 's':
      return 'seconds';
    default:
      return 'count';
  }
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1);
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
}

export function formatDurationSeconds(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '0s';
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) return `${minutes}m`;
  return `${Math.floor(seconds)}s`;
}

export function formatTrimmedNumber(value: number): string {
  if (!Number.isFinite(value)) return '—';
  if (Number.isInteger(value)) return String(value);
  const abs = Math.abs(value);
  const decimals = abs >= 100 ? 0 : abs >= 1 ? 2 : 3;
  return String(Number(value.toFixed(decimals)));
}

// Formats a metric value in its native unit kind.
export function formatMetricValue(value: number, unit: MetricUnitKind): string {
  switch (unit) {
    case 'ratio':
      return `${(value * 100).toFixed(1)}%`;
    case 'bytes':
      return formatBytes(value);
    case 'seconds':
      return formatDurationSeconds(value);
    default:
      return formatTrimmedNumber(value);
  }
}

// Formats a host_metric alert value. Legacy kinds ('cpu'…) carry percent
// values already; new alerts carry NATIVE units (ratio 0-1 for utilization).
export function formatAlertValue(metricNameOrKey: string | undefined, value: number): string {
  if (!metricNameOrKey) return formatTrimmedNumber(value);
  const { name } = parseSeriesKey(metricNameOrKey);
  const meta = METRIC_META[name];
  if (meta?.legacyPercent) {
    return `${formatTrimmedNumber(value)}%`;
  }
  return formatMetricValue(value, metricUnitKind(name));
}

// Picks the most descriptive attribute for a series label:
// mountpoint > direction > device > first non-'state' attribute.
export function displayAttr(attributes: Record<string, string>): string | null {
  if (attributes.mountpoint) return attributes.mountpoint;
  if (attributes.direction) return attributes.direction;
  if (attributes.device) return attributes.device;
  for (const [key, value] of Object.entries(attributes)) {
    if (key !== 'state') return value;
  }
  return null;
}

// "Filesystem (/data)"-style label for a canonical series key.
export function formatSeriesLabel(seriesKey: string | undefined): string {
  if (!seriesKey) return 'host metric';
  const { name, attributes } = parseSeriesKey(seriesKey);
  const meta = METRIC_META[name];
  const base = meta?.short || name;
  const attr = displayAttr(attributes);
  return attr ? `${base} (${attr})` : base;
}
