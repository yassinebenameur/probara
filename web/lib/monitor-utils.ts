import { formatLatency, formatRelativeTime } from './format';
import { CheckResult, HTTPDisplayValue, Monitor, MonitorState, MonitorType } from './types';

export type MonitorHealthStatus = 'up' | 'down' | 'degraded' | 'unknown';
export type MonitorDisplayStatus = MonitorHealthStatus | 'paused' | 'maintenance';

// Backward-compatible fallback while older rows/API payloads may miss result_source.
const EXPIRED_JOB_MESSAGE = 'Job expired before processing';

export function isPlatformResult(result: CheckResult): boolean {
  if (result.result_source) {
    return result.result_source === 'platform';
  }
  return result.error_message === EXPIRED_JOB_MESSAGE;
}

export function getOperationalResults(results: CheckResult[]): CheckResult[] {
  if (!results || results.length === 0) return [];
  return results.filter((r) => !isPlatformResult(r));
}

/**
 * Display values (HTTP `display_fields`) from the latest monitor-sourced
 * result. Platform results (expired jobs) carry no body, so they are skipped
 * rather than blanking the chips; a real failed check does blank them.
 */
export function getHTTPDisplayValues(results: CheckResult[]): HTTPDisplayValue[] {
  const latest = getOperationalResults(results)[0];
  const metrics = latest?.metrics_data as { http?: { display_values?: unknown } } | undefined;
  const values = metrics?.http?.display_values;
  if (!Array.isArray(values)) return [];
  return values.filter(
    (v): v is HTTPDisplayValue =>
      !!v && typeof v === 'object' && typeof v.path === 'string' && typeof v.value === 'string'
  );
}

/** Chip label for a display value; empty means the chip shows the value alone. */
export function displayValueLabel(v: HTTPDisplayValue): string {
  return v.label?.trim() || '';
}

export function countOperationalResults(results: CheckResult[]): number {
  return getOperationalResults(results).length;
}

/**
 * Calculate uptime percentage from check results
 * @param results Array of check results
 * @returns Uptime percentage (0-100)
 */
export function calculateUptime(results: CheckResult[]): number {
  const operationalResults = getOperationalResults(results);
  if (operationalResults.length === 0) return 0;

  const successCount = operationalResults.filter((r) => r.status === 'success').length;
  return (successCount / operationalResults.length) * 100;
}

/**
 * Format interval seconds into human-readable string
 * @param seconds Interval in seconds
 * @returns Formatted string like "Every 30s" or "Every 5 min"
 */
export function formatInterval(seconds: number): string {
  if (seconds < 60) {
    return `Every ${seconds}s`;
  } else if (seconds < 3600) {
    const minutes = Math.floor(seconds / 60);
    return `Every ${minutes} min`;
  } else {
    const hours = Math.floor(seconds / 3600);
    return `Every ${hours} hr`;
  }
}

/**
 * Get the latest status from check results
 * @param results Array of check results
 * @returns Status indicator
 */
export function getLatestStatus(results: CheckResult[]): MonitorHealthStatus {
  const operationalResults = getOperationalResults(results);
  if (operationalResults.length === 0) return 'unknown';

  const latest = operationalResults[0]; // Assuming results are sorted by created_at desc
  
  // Handle explicit status values from API (including synthetic group status)
  if (latest.status === 'success') {
    return 'up';
  } else if (latest.status === 'degraded') {
    return 'degraded';
  } else if (latest.status === 'failure' || latest.status === 'error') {
    // Check if we have recent failures to determine degraded vs down
    const recentResults = operationalResults.slice(0, 5);
    const failureCount = recentResults.filter(
      (r) => r.status === 'failure' || r.status === 'error'
    ).length;
    
    // If less than half are failures, consider it degraded
    if (failureCount < recentResults.length / 2) {
      return 'degraded';
    }
    return 'down';
  }

  return 'unknown';
}

export function getEffectiveMonitorStatus(
  monitor: Pick<Monitor, 'enabled' | 'in_maintenance' | 'current_state' | 'location_ids'>,
  results: CheckResult[]
): MonitorDisplayStatus {
  if (!monitor.enabled) {
    return 'paused';
  }
  if (monitor.in_maintenance) {
    return 'maintenance';
  }

  // Multi-location monitors: the server's quorum verdict is authoritative —
  // per-result derivation would misread a below-quorum location failure as down.
  if (monitor.location_ids?.length && monitor.current_state) {
    switch (monitor.current_state) {
      case 'up':
        return 'up';
      case 'down':
        return 'down';
      case 'degraded':
      case 'suspect':
        return 'degraded';
      default:
        return 'unknown';
    }
  }

  return getLatestStatus(results);
}

/**
 * Tailwind color classes for a monitor's current_state, used by the
 * dependency UI (status dots, graph nodes). Tones match AlertTable.
 */
export function monitorStateColors(state?: MonitorState): { dot: string; text: string; border: string } {
  switch (state) {
    case 'up':
      return { dot: 'bg-emerald-500', text: 'text-emerald-300', border: 'border-emerald-500/40' };
    case 'suspect':
      return { dot: 'bg-amber-500', text: 'text-amber-300', border: 'border-amber-500/40' };
    case 'degraded':
      return { dot: 'bg-amber-500', text: 'text-amber-300', border: 'border-amber-500/40' };
    case 'down':
      return { dot: 'bg-rose-500', text: 'text-rose-300', border: 'border-rose-500/40' };
    default:
      return { dot: 'bg-slate-500', text: 'text-slate-400', border: 'border-slate-500/40' };
  }
}

/**
 * Human name for a monitor's target, used in copy/open affordances so the
 * tooltip reads "Copy URL" for HTTP but "Copy host" for ping and friends.
 */
export function monitorTargetLabel(type?: MonitorType | string): string {
  switch (type) {
    case 'http':
    case 'synthetic_api':
    case 'synthetic_browser':
    case 'prometheus':
    case 'websocket':
      return 'URL';
    case 'ping':
    case 'dns':
    case 'agent':
      return 'host';
    case 'sip':
      return 'SIP target';
    default:
      return 'endpoint';
  }
}

/**
 * Relative time ("12s ago", "3 min ago"). Kept as an alias so existing
 * imports keep working; the implementation is formatRelativeTime in
 * lib/format.ts.
 */
export function formatTimeAgo(timestamp: string): string {
  return formatRelativeTime(timestamp);
}

/**
 * Latency statistics over successful operational checks. Failed checks are
 * left out: their latency is usually the timeout (a down Mongo reads
 * "10000ms") or a fast refusal that looks healthy. All zeros, with
 * `samples: 0`, when there is nothing to measure; render that as "—".
 */
export function calculateLatencyStats(results: CheckResult[]): {
  median: number;
  p95: number;
  latest: number;
  samples: number;
} {
  const measured = getOperationalResults(results).filter(
    (r) => r.status === 'success' && r.latency_ms !== undefined && r.latency_ms !== null
  );
  if (measured.length === 0) {
    return { median: 0, p95: 0, latest: 0, samples: 0 };
  }

  const latencies = measured.map((r) => r.latency_ms!);
  const sorted = [...latencies].sort((a, b) => a - b);
  // Results arrive newest first.
  const latest = latencies[0];

  const mid = Math.floor(sorted.length / 2);
  const median =
    sorted.length % 2 === 0 ? (sorted[mid - 1] + sorted[mid]) / 2 : sorted[mid];

  const p95Index = Math.ceil(sorted.length * 0.95) - 1;
  const p95 = sorted[p95Index];

  return {
    median: Math.round(median),
    p95: Math.round(p95),
    latest: Math.round(latest),
    samples: sorted.length,
  };
}

// Severity order for lists: problems first, then healthy, then the states an
// operator chose (maintenance, paused). Sort by this, then by name.
export const STATUS_RANK: Record<MonitorDisplayStatus, number> = {
  down: 0,
  degraded: 1,
  unknown: 2,
  up: 3,
  maintenance: 4,
  paused: 5,
};

/** Stable severity-then-name sort; `getStatus` maps an item to its display status. */
export function sortBySeverity<T>(
  items: readonly T[],
  getStatus: (item: T) => MonitorDisplayStatus,
  getName: (item: T) => string
): T[] {
  return [...items].sort(
    (a, b) =>
      STATUS_RANK[getStatus(a)] - STATUS_RANK[getStatus(b)] ||
      getName(a).localeCompare(getName(b), undefined, { sensitivity: 'base' })
  );
}

/**
 * The address a monitor checks, as shown under its name: the URL for HTTP,
 * synthetic, Prometheus and WebSocket monitors, `host:port` for gRPC and TCP,
 * the host for everything else. Null when the type has no target (push,
 * group, agent without a host).
 */
export function monitorTarget(monitor: Pick<Monitor, 'type' | 'config' | 'url'>): string | null {
  const config = monitor.config as Record<string, unknown> | undefined;
  const str = (key: string) => {
    const v = config?.[key];
    return typeof v === 'string' && v.trim() ? v : null;
  };
  if (config) {
    if ('url' in config) return str('url');
    if ('base_url' in config) return str('base_url');
    if ('start_url' in config) return str('start_url');
    const host = str('host');
    if (monitor.type === 'grpc' && host) {
      const port = (config.port as number | undefined) || (config.use_tls === false ? 80 : 443);
      return `${host}:${port}`;
    }
    if (monitor.type === 'tcp' && host) {
      return config.port ? `${host}:${config.port}` : host;
    }
    if (monitor.type === 'sip' && host) {
      // Under RFC 3263 without a port, SRV picks the servers: the target is
      // the domain. Otherwise the port the worker dials (5061 over TLS).
      if (config.resolution === 'rfc3263' && !config.port) return host;
      const tls = typeof config.transport === 'string' && config.transport.toLowerCase() === 'tls';
      return `${host}:${(config.port as number | undefined) || (tls ? 5061 : 5060)}`;
    }
  }
  if (monitor.url) return monitor.url;
  return str('host');
}

const HTTP_METHOD_PREFIX = /^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|Get|Post|Put|Patch|Delete|Head|Options) "[^"]*":\s*/;
// Category prefixes the HTTP checker writes ("connect: …", "status_code: …").
const CATEGORY_PREFIX = /^(timeout|dns|connect|status_code|latency|tls|unknown|body_read|request_creation|invalid_url)\s*:\s*/i;

// Assertion failures the checkers write ("body: missing \"timeout\"", "header
// X-Id: != \"1\"", "query_value: …"). They quote the operator's own expected
// values, so the transport keyword matching below must never run on them: a
// body assertion about "timeout" is not a timeout. Shown verbatim.
const ASSERTION_PREFIX = /^(body|body_assertions|json_assertions|header(\s+[^:\s]+)?|expected_substring|query_value)\s*:/i;

function shortenOne(raw: string, timeoutSeconds?: number): string {
  let text = raw.trim();
  if (ASSERTION_PREFIX.test(text)) return text;
  const category = CATEGORY_PREFIX.exec(text)?.[1]?.toLowerCase();
  if (category && category !== 'tls') text = text.replace(CATEGORY_PREFIX, '');
  text = text.replace(HTTP_METHOD_PREFIX, '');

  const status = /^got (\d{3})\b/.exec(text) ?? (category === 'status_code' ? /(\d{3})/.exec(text) : null);
  if (status) return `HTTP ${status[1]}`;

  const slow = /^(\d+)ms > (\d+)ms/.exec(text);
  if (category === 'latency' && slow) {
    return `slow response (${formatLatency(Number(slow[1]))}, limit ${formatLatency(Number(slow[2]))})`;
  }

  const lower = text.toLowerCase();
  if (lower.includes('certificate has expired')) return 'TLS certificate expired';
  if (lower.includes('certificate signed by unknown authority')) return 'TLS certificate not trusted';
  if (lower.includes('certificate is valid for') || lower.includes('certificate is not valid for')) {
    return 'TLS certificate name mismatch';
  }
  if (lower.includes('connection refused')) return 'connection refused';
  if (lower.includes('connection reset')) return 'connection reset';
  if (lower.includes('no such host')) return 'no such host';
  if (lower.includes('no route to host')) return 'no route to host';
  if (lower.includes('network is unreachable')) return 'network unreachable';
  if (category === 'timeout' || /deadline exceeded|timed? ?out|timeout/.test(lower)) {
    return timeoutSeconds ? `timed out after ${timeoutSeconds} s` : 'timed out';
  }
  if (/(^|: )eof$/.test(lower)) return 'connection closed';

  // Drop the dialler noise ("dial tcp 10.0.0.1:443: ") and keep the cause.
  return text.replace(/dial (tcp|udp)[46]? (\[[^\]]*\]|[^:\s]*)(:\d+)?:\s*/g, '').replace(/^connect:\s*/, '') || raw.trim();
}

/**
 * A short, human reason for a failed check: `connection refused`,
 * `HTTP 503`, `timed out after 10 s`. Strips Go's
 * `Get "url": dial tcp addr: connect:` framing, which repeats the monitor's
 * own target and pushes the cause past any truncation. Show the raw message
 * in a `title` tooltip next to it. Several assertion failures keep the first
 * and count the rest. Empty when there is nothing to say.
 */
export function shortFailureReason(
  raw: string | null | undefined,
  opts: { httpStatus?: number | null; timeoutSeconds?: number } = {}
): string {
  const text = raw?.trim();
  if (!text) return opts.httpStatus && opts.httpStatus >= 400 ? `HTTP ${opts.httpStatus}` : '';
  const parts = text.split('; ').filter(Boolean);
  const first = shortenOne(parts[0], opts.timeoutSeconds);
  if (parts.length === 1) return first;
  // Name the other failed assertions ("HTTP 503 + latency") rather than an
  // opaque "+1 more"; the raw text stays in the caller's title tooltip.
  const others = parts.slice(1).map((p) => /^([a-z_]+):/i.exec(p)?.[1]?.replace(/_/g, ' '));
  if (others.every(Boolean)) return `${first} + ${[...new Set(others)].join(', ')}`;
  return `${first} + ${parts.length - 1} more`;
}
