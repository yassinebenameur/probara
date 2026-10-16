'use client';

import { useEffect, useRef, useState } from 'react';

import { getMonitorMetricsSnapshots } from '@/lib/api';
import type { MonitorAnalyticsRange, MonitorMetricsSnapshotsResponse } from '@/lib/types';

/**
 * The monitor's metrics_data snapshots over the overview's range, refetched
 * when a newer check lands (latestResultAt is the newest known check's
 * created_at). Null until the first response.
 *
 * New checks refetch at most once per bucket: a newer snapshot inside the
 * current bucket would only replace the last point. Superseded responses are
 * dropped by sequence, not effect cleanup, so a throttled re-run never
 * discards the fetch still in flight.
 */
export function useMetricsSnapshots(
  monitorId: string,
  range: MonitorAnalyticsRange,
  latestResultAt: string | undefined,
  label: string
): MonitorMetricsSnapshotsResponse | null {
  const [data, setData] = useState<MonitorMetricsSnapshotsResponse | null>(null);
  const lastFetch = useRef<{ key: string; at: number; bucketMs: number } | null>(null);
  const fetchSeq = useRef(0);

  useEffect(() => {
    const key = `${monitorId}:${range}`;
    const prev = lastFetch.current;
    if (prev && prev.key === key && Date.now() - prev.at < prev.bucketMs) return;
    lastFetch.current = { key, at: Date.now(), bucketMs: prev?.key === key ? prev.bucketMs : 0 };
    const seq = ++fetchSeq.current;
    getMonitorMetricsSnapshots(monitorId, { range })
      .then((resp) => {
        if (seq !== fetchSeq.current) return;
        lastFetch.current = { key, at: Date.now(), bucketMs: resp.bucket_seconds * 1000 };
        setData(resp);
      })
      .catch((err) => console.error(`Failed to load ${label}:`, err));
  }, [monitorId, range, latestResultAt, label]);

  return data;
}
