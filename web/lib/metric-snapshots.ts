import type { MonitorMetricsSnapshotsResponse } from './types';

// Helpers for the per-check metrics_data panels on the monitor detail page
// (MongoClusterPanels, SipCallPanels), which read GET
// /monitors/{id}/metrics-snapshots: one snapshot per check, downsampled
// server-side to the newest per bucket_seconds bucket over long ranges.

/** One check's snapshot, its payload picked out of metrics_data. */
export interface TimedSnapshot<T> {
  ts: number;
  m: T;
}

/**
 * The snapshots that carry a payload, oldest first as the endpoint returns
 * them. pick reads the type's own key (metrics_data.mongodb, .sip, …) and
 * returns undefined for a check that recorded none.
 */
export function pickSnapshots<T>(
  resp: MonitorMetricsSnapshotsResponse | null | undefined,
  pick: (metricsData: unknown) => T | null | undefined
): TimedSnapshot<T>[] {
  const out: TimedSnapshot<T>[] = [];
  for (const s of resp?.snapshots ?? []) {
    const m = pick(s.metrics_data);
    if (m === undefined || m === null) continue;
    const ts = Date.parse(s.created_at);
    if (Number.isFinite(ts)) out.push({ ts, m });
  }
  return out;
}

/**
 * The chart grid step for snapshot points (metric-chart buildChartRows).
 *
 * Rows snap to the observed check cadence (median inter-snapshot delta), not
 * only the configured interval: scheduler ticks land with jitter (e.g. 35s
 * on a 30s interval), and a too-fine grid turns that jitter into false line
 * breaks. A genuinely missed check still leaves a >1-bucket hole, so real
 * gaps keep breaking the line. The 1.25 pad absorbs fractional drift (35s
 * points on a 35s grid would still skip a bucket now and then); two checks in
 * one bucket just overwrite, which is invisible. Downsampled, the
 * newest-per-bucket picks sit up to one bucket plus one check interval
 * apart, so the grid must be that coarse or it breaks the line between
 * adjacent buckets.
 */
export function snapshotStepMs(timestamps: number[], intervalSeconds: number, bucketSeconds: number): number {
  const deltas = timestamps
    .slice(1)
    .map((ts, i) => ts - timestamps[i])
    .sort((a, b) => a - b);
  const medianDelta = deltas[Math.floor(deltas.length / 2)] ?? 0;
  const intervalMs = Math.max(0, intervalSeconds) * 1000;
  const bucketMs = Math.max(0, bucketSeconds) * 1000;
  return Math.max(intervalMs, Math.ceil(medianDelta * 1.25), bucketMs > intervalMs ? bucketMs + intervalMs : 0, 1000);
}
