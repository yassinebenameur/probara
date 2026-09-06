import { CheckResult, Monitor, MonitorState, MonitorType } from './types';

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
      return { dot: 'bg-emerald-500', text: 'text-emerald-400', border: 'border-emerald-500/40' };
    case 'suspect':
      return { dot: 'bg-amber-500', text: 'text-amber-400', border: 'border-amber-500/40' };
    case 'degraded':
      return { dot: 'bg-amber-500', text: 'text-amber-400', border: 'border-amber-500/40' };
    case 'down':
      return { dot: 'bg-rose-500', text: 'text-rose-400', border: 'border-rose-500/40' };
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
 * Format timestamp into relative time string
 * @param timestamp ISO timestamp string
 * @returns Relative time string like "4s ago" or "2 min ago"
 */
export function formatTimeAgo(timestamp: string): string {
  const now = new Date().getTime();
  const then = new Date(timestamp).getTime();
  const diffMs = now - then;
  const diffSeconds = Math.floor(diffMs / 1000);

  if (diffSeconds < 60) {
    return `${diffSeconds}s ago`;
  } else if (diffSeconds < 3600) {
    const minutes = Math.floor(diffSeconds / 60);
    return `${minutes} min ago`;
  } else if (diffSeconds < 86400) {
    const hours = Math.floor(diffSeconds / 3600);
    return `${hours} hr ago`;
  } else {
    const days = Math.floor(diffSeconds / 86400);
    return `${days} day${days > 1 ? 's' : ''} ago`;
  }
}

/**
 * Calculate latency statistics from check results
 * @param results Array of check results
 * @returns Object with median and p95 latency in ms
 */
export function calculateLatencyStats(results: CheckResult[]): {
  median: number;
  p95: number;
  latest: number;
} {
  const operationalResults = getOperationalResults(results);
  if (operationalResults.length === 0) {
    return { median: 0, p95: 0, latest: 0 };
  }

  const latencies = operationalResults
    .filter((r) => r.latency_ms !== undefined && r.latency_ms !== null)
    .map((r) => r.latency_ms!);

  if (latencies.length === 0) {
    return { median: 0, p95: 0, latest: 0 };
  }

  const sorted = [...latencies].sort((a, b) => a - b);
  const latest = operationalResults.find((r) => r.latency_ms !== undefined && r.latency_ms !== null)?.latency_ms ?? 0;

  // Calculate median
  const mid = Math.floor(sorted.length / 2);
  const median =
    sorted.length % 2 === 0 ? (sorted[mid - 1] + sorted[mid]) / 2 : sorted[mid];

  // Calculate P95
  const p95Index = Math.ceil(sorted.length * 0.95) - 1;
  const p95 = sorted[p95Index];

  return {
    median: Math.round(median),
    p95: Math.round(p95),
    latest: Math.round(latest),
  };
}
