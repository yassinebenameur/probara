import assert from 'node:assert/strict';
import test from 'node:test';
import { pickSnapshots, snapshotStepMs } from './metric-snapshots';
import type { MonitorMetricsSnapshotsResponse } from './types';

const resp = (snapshots: MonitorMetricsSnapshotsResponse['snapshots']): MonitorMetricsSnapshotsResponse => ({
  monitor_id: 'm1',
  range: '24h',
  bucket_seconds: 0,
  coverage_start: '2026-10-15T10:00:00Z',
  is_partial: false,
  snapshots,
});

test('pickSnapshots keeps snapshots with the picked payload and a valid time', () => {
  const picked = pickSnapshots(
    resp([
      { created_at: '2026-10-16T10:00:00Z', metrics_data: { sip: { method: 'INVITE' } } },
      { created_at: '2026-10-16T10:01:00Z', metrics_data: null },
      { created_at: 'not a date', metrics_data: { sip: { method: 'INVITE' } } },
      { created_at: '2026-10-16T10:02:00Z', metrics_data: { http: {} } },
      { created_at: '2026-10-16T10:03:00Z', metrics_data: { sip: { method: 'INVITE' } } },
    ]),
    (md) => (md as { sip?: { method: string } } | null)?.sip
  );
  assert.deepEqual(
    picked.map((p) => p.ts),
    [Date.parse('2026-10-16T10:00:00Z'), Date.parse('2026-10-16T10:03:00Z')]
  );
  assert.deepEqual(pickSnapshots(null, () => 1), []);
});

test('snapshotStepMs follows the observed cadence, the interval and the bucket', () => {
  // Raw snapshots on a 60s interval: the interval wins over a tight cadence.
  assert.equal(snapshotStepMs([0, 60_000, 120_000], 60, 0), 75_000);
  // Jittered ticks (70s on a 60s interval): the median delta, padded.
  assert.equal(snapshotStepMs([0, 70_000, 140_000, 210_000], 60, 0), 87_500);
  // Downsampled into 600s buckets: one bucket plus one interval.
  assert.equal(snapshotStepMs([0, 500_000], 60, 600), 660_000);
  // Nothing to measure: the interval, never below 1s.
  assert.equal(snapshotStepMs([], 60, 0), 60_000);
  assert.equal(snapshotStepMs([], 0, 0), 1000);
});
