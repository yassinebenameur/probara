import { CheckResult } from './types';

/**
 * Calculate uptime percentage from check results
 * @param results Array of check results
 * @returns Uptime percentage (0-100)
 */
export function calculateUptime(results: CheckResult[]): number {
  if (!results || results.length === 0) return 0;

  const successCount = results.filter((r) => r.status === 'success').length;
  return (successCount / results.length) * 100;
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
export function getLatestStatus(results: CheckResult[]): 'up' | 'down' | 'degraded' {
  if (!results || results.length === 0) return 'down';

  const latest = results[0]; // Assuming results are sorted by created_at desc
  
  // Handle explicit status values from API (including synthetic group status)
  if (latest.status === 'success') {
    return 'up';
  } else if (latest.status === 'degraded') {
    return 'degraded';
  } else if (latest.status === 'failure' || latest.status === 'error') {
    // Check if we have recent failures to determine degraded vs down
    const recentResults = results.slice(0, 5);
    const failureCount = recentResults.filter(
      (r) => r.status === 'failure' || r.status === 'error'
    ).length;
    
    // If less than half are failures, consider it degraded
    if (failureCount < recentResults.length / 2) {
      return 'degraded';
    }
    return 'down';
  }

  return 'down';
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
  if (!results || results.length === 0) {
    return { median: 0, p95: 0, latest: 0 };
  }

  const latencies = results
    .filter((r) => r.latency_ms !== undefined && r.latency_ms !== null)
    .map((r) => r.latency_ms!);

  if (latencies.length === 0) {
    return { median: 0, p95: 0, latest: 0 };
  }

  const sorted = [...latencies].sort((a, b) => a - b);
  const latest = results[0]?.latency_ms ?? 0;

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

