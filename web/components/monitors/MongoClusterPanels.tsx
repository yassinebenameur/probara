'use client';

// Charts for the MongoDB cluster checks (clusterMonitor role). Each check
// result carries one snapshot in metrics_data.mongodb; gauges chart directly,
// cumulative counters (opcounters, network bytes) chart as per-second rates
// between consecutive snapshots. Snapshots come from the metrics-snapshots
// endpoint for the overview's selected range — downsampled server-side to the
// newest per bucket, which keeps counter rates exact (a delta across skipped
// checks is their average rate) while gauges become point samples. Reuses
// the metric-store chart internals so the panels match the agent dashboard.

import { useMemo } from 'react';
import { Monitor, MonitorAnalyticsRange, MongoDBMetrics, MongoDBMonitorConfig } from '@/lib/types';
import { formatCount } from '@/lib/format';
import { formatBytes } from '@/lib/metrics';
import { pickSnapshots, snapshotStepMs, type TimedSnapshot } from '@/lib/metric-snapshots';
import { useMetricsSnapshots } from '@/lib/useMetricsSnapshots';
import Callout from '@/components/ui/Callout';
import InfoTip from '@/components/ui/InfoTip';
import {
  ChartTile,
  MetricChart,
  buildChartRows,
  SERIES_COLORS,
  type ChartRow,
  type ChartSeries,
  type ReferenceThreshold,
  chartRangeFor,
} from './metric-chart';

type Snapshot = TimedSnapshot<MongoDBMetrics>;

interface PanelSpec {
  title: string;
  headline: string;
  rows: ChartRow[];
  series: ChartSeries[];
  yTickFormatter: (v: number) => string;
  valueFormatter: (v: number) => string;
  yDomain?: [number | string, number | string];
  referenceLines?: ReferenceThreshold[];
  yAxisWidth?: number;
}

// Byte tick labels ("953.7 MB") need more room than the default axis width.
const BYTES_AXIS_WIDTH = 76;

type Point = [number, number];

const secondsValue = (v: number) => `${Math.round(v)}s`;
const countValue = (v: number) => formatCount(v);
const bytesTick = (v: number) => formatBytes(v);
const rateValue = (v: number) => `${formatBytes(v)}/s`;
const opsValue = (v: number) => `${v >= 10 ? Math.round(v) : v.toFixed(1)}/s`;
const cpuValue = (v: number) => `${v >= 100 ? Math.round(v) : v.toFixed(1)}%`;

// Per-second rate between consecutive snapshots of a cumulative counter.
// Negative deltas mean the server restarted — that interval is skipped.
function ratePoints(snapshots: Snapshot[], read: (m: MongoDBMetrics) => number | undefined): Point[] {
  const points: Point[] = [];
  let prev: { ts: number; value: number } | null = null;
  for (const { ts, m } of snapshots) {
    const value = read(m);
    if (value === undefined) continue;
    if (prev && ts > prev.ts && value >= prev.value) {
      points.push([ts, (value - prev.value) / ((ts - prev.ts) / 1000)]);
    }
    prev = { ts, value };
  }
  return points;
}

function gaugePoints(snapshots: Snapshot[], read: (m: MongoDBMetrics) => number | undefined): Point[] {
  const points: Point[] = [];
  for (const { ts, m } of snapshots) {
    const value = read(m);
    if (value !== undefined) points.push([ts, value]);
  }
  return points;
}

const last = (points: Point[]): number | undefined => points[points.length - 1]?.[1];

export default function MongoClusterPanels({
  monitor,
  timeRange,
  latestResultAt,
}: {
  monitor: Monitor;
  timeRange: MonitorAnalyticsRange;
  // created_at of the newest known check; a change triggers a refetch.
  latestResultAt?: string;
}) {
  const config = (monitor.config || {}) as MongoDBMonitorConfig;
  const data = useMetricsSnapshots(monitor.id, timeRange, latestResultAt, 'cluster metrics');

  const { panels, unauthorized } = useMemo(() => {
    const snapshots: Snapshot[] = pickSnapshots(data, (md) => (md as { mongodb?: MongoDBMetrics } | null)?.mongodb);

    const latest = snapshots[snapshots.length - 1]?.m;
    const isUnauthorized = Boolean(latest?.unavailable?.some((u) => u.reason === 'unauthorized'));

    const stepMs = snapshotStepMs(
      snapshots.map((snap) => snap.ts),
      monitor.interval_seconds,
      data?.bucket_seconds ?? 0
    );
    const specs: PanelSpec[] = [];

    const lag = gaugePoints(snapshots, (m) => m.replication?.max_lag_seconds);
    if (lag.length > 0) {
      const referenceLines: ReferenceThreshold[] = [];
      if (config.warn_replication_lag_seconds) {
        referenceLines.push({ y: config.warn_replication_lag_seconds, label: `warn ${config.warn_replication_lag_seconds}s` });
      }
      if (config.max_replication_lag_seconds) {
        referenceLines.push({ y: config.max_replication_lag_seconds, label: `fail ${config.max_replication_lag_seconds}s` });
      }
      specs.push({
        title: 'Replication lag',
        headline: `${Math.round(last(lag) ?? 0)}s`,
        rows: buildChartRows([{ key: 'lag', points: lag }], stepMs),
        series: [{ key: 'lag', name: 'Max lag', color: SERIES_COLORS[0] }],
        yTickFormatter: secondsValue,
        valueFormatter: secondsValue,
        yDomain: [0, 'auto'],
        referenceLines: referenceLines.length ? referenceLines : undefined,
      });
    }

    const connections = gaugePoints(snapshots, (m) => m.connected_clients ?? undefined);
    if (connections.length > 0) {
      const available = latest?.connections_available;
      specs.push({
        title: 'Connections',
        headline: `${countValue(last(connections) ?? 0)}${available != null ? ` / ${countValue(available)} avail` : ''}`,
        rows: buildChartRows([{ key: 'conns', points: connections }], stepMs),
        series: [{ key: 'conns', name: 'Current', color: SERIES_COLORS[2] }],
        yTickFormatter: countValue,
        valueFormatter: countValue,
        yDomain: [0, 'auto'],
      });
    }

    const cacheUsed = gaugePoints(snapshots, (m) => m.cache_used_bytes);
    if (cacheUsed.length > 0) {
      const cacheDirty = gaugePoints(snapshots, (m) => m.cache_dirty_bytes);
      const max = latest?.cache_max_bytes;
      specs.push({
        title: 'WiredTiger cache',
        headline: `${formatBytes(last(cacheUsed) ?? 0)}${max != null ? ` / ${formatBytes(max)}` : ''}`,
        rows: buildChartRows(
          [
            { key: 'used', points: cacheUsed },
            { key: 'dirty', points: cacheDirty },
          ],
          stepMs
        ),
        series: [
          { key: 'used', name: 'Used', color: SERIES_COLORS[2] },
          { key: 'dirty', name: 'Dirty', color: SERIES_COLORS[1] },
        ],
        yTickFormatter: bytesTick,
        valueFormatter: bytesTick,
        yDomain: [0, 'auto'],
        yAxisWidth: BYTES_AXIS_WIDTH,
      });
    }

    const memResident = gaugePoints(snapshots, (m) => m.used_memory_bytes);
    const memVirtual = gaugePoints(snapshots, (m) => m.mem_virtual_bytes);
    if (memResident.length > 0 || memVirtual.length > 0) {
      specs.push({
        title: 'Memory',
        headline: memResident.length > 0 ? formatBytes(last(memResident) ?? 0) : formatBytes(last(memVirtual) ?? 0),
        rows: buildChartRows(
          [
            { key: 'resident', points: memResident },
            { key: 'virtual', points: memVirtual },
          ],
          stepMs
        ),
        series: [
          { key: 'resident', name: 'Resident', color: SERIES_COLORS[3] },
          { key: 'virtual', name: 'Virtual', color: SERIES_COLORS[4] },
        ],
        yTickFormatter: bytesTick,
        valueFormatter: bytesTick,
        yDomain: [0, 'auto'],
        yAxisWidth: BYTES_AXIS_WIDTH,
      });
    }

    const reads = ratePoints(snapshots, (m) =>
      m.opcounters ? (m.opcounters.query ?? 0) + (m.opcounters.getmore ?? 0) : undefined
    );
    if (reads.length > 0) {
      const writes = ratePoints(snapshots, (m) =>
        m.opcounters ? (m.opcounters.insert ?? 0) + (m.opcounters.update ?? 0) + (m.opcounters.delete ?? 0) : undefined
      );
      const commands = ratePoints(snapshots, (m) => m.opcounters?.command);
      specs.push({
        title: 'Operations',
        headline: opsValue((last(reads) ?? 0) + (last(writes) ?? 0) + (last(commands) ?? 0)),
        rows: buildChartRows(
          [
            { key: 'reads', points: reads },
            { key: 'writes', points: writes },
            { key: 'commands', points: commands },
          ],
          stepMs
        ),
        series: [
          { key: 'reads', name: 'Reads', color: SERIES_COLORS[2] },
          { key: 'writes', name: 'Writes', color: SERIES_COLORS[1] },
          { key: 'commands', name: 'Commands', color: SERIES_COLORS[4] },
        ],
        yTickFormatter: opsValue,
        valueFormatter: opsValue,
        yDomain: [0, 'auto'],
      });
    }

    // Process CPU: cumulative µs of CPU time → % of one core (can exceed
    // 100% on a multi-threaded mongod, like top).
    const toCorePercent = (points: Point[]): Point[] => points.map(([ts, v]) => [ts, (v / 1_000_000) * 100]);
    const cpuUser = toCorePercent(ratePoints(snapshots, (m) => m.cpu_user_us));
    if (cpuUser.length > 0) {
      const cpuSystem = toCorePercent(ratePoints(snapshots, (m) => m.cpu_system_us));
      specs.push({
        title: 'Process CPU',
        headline: `${cpuValue((last(cpuUser) ?? 0) + (last(cpuSystem) ?? 0))} of one core`,
        rows: buildChartRows(
          [
            { key: 'user', points: cpuUser },
            { key: 'system', points: cpuSystem },
          ],
          stepMs
        ),
        series: [
          { key: 'user', name: 'User', color: SERIES_COLORS[1] },
          { key: 'system', name: 'System', color: SERIES_COLORS[2] },
        ],
        yTickFormatter: cpuValue,
        valueFormatter: cpuValue,
        yDomain: [0, 'auto'],
      });
    }

    const netIn = ratePoints(snapshots, (m) => m.network_bytes_in);
    if (netIn.length > 0) {
      const netOut = ratePoints(snapshots, (m) => m.network_bytes_out);
      specs.push({
        title: 'Network',
        headline: `${rateValue(last(netIn) ?? 0)} in`,
        rows: buildChartRows(
          [
            { key: 'in', points: netIn },
            { key: 'out', points: netOut },
          ],
          stepMs
        ),
        series: [
          { key: 'in', name: 'In', color: SERIES_COLORS[2] },
          { key: 'out', name: 'Out', color: SERIES_COLORS[0] },
        ],
        yTickFormatter: rateValue,
        valueFormatter: rateValue,
        yDomain: [0, 'auto'],
        yAxisWidth: BYTES_AXIS_WIDTH,
      });
    }

    return { panels: specs, unauthorized: isUnauthorized };
  }, [data, monitor.interval_seconds, config.warn_replication_lag_seconds, config.max_replication_lag_seconds]);

  // The range window, clamped like the snapshots to raw retention, so the
  // charts line up with the response-time chart above them.
  const xDomain = useMemo<[number, number] | undefined>(() => {
    const start = data ? Date.parse(data.coverage_start) : NaN;
    return Number.isFinite(start) ? [start, Date.now()] : undefined;
  }, [data]);

  if (panels.length === 0 && !unauthorized) return null;

  return (
    <section className="glass p-5">
      <div className="mb-4 flex items-center gap-1.5">
        <h3 className="text-title font-semibold text-white">Cluster metrics</h3>
        <InfoTip inLabel ariaLabel="About cluster metrics">
          One snapshot per check from the enabled cluster checks (serverStatus / replSetGetStatus, read with the
          clusterMonitor role). Operation and network counters are cumulative on the server and shown here as
          per-second rates between checks. Longer ranges keep one snapshot per time bucket, so gauges show point
          samples while rates stay averaged across the skipped checks.
        </InfoTip>
        {data?.is_partial && (
          <span className="ml-auto text-xs text-slate-400">Last 30 days — raw check retention</span>
        )}
      </div>
      {unauthorized && (
        <Callout tone="warning" className={panels.length > 0 ? 'mb-4' : undefined}>
          Cluster checks are enabled but skipped — grant the monitoring user the clusterMonitor role.
        </Callout>
      )}
      {/* Three across from 3xl, the detail page's widest layout (it caps at
          100rem, so 1920 and 2560 look alike). */}
      <div className="grid gap-4 lg:grid-cols-2 3xl:grid-cols-3">
        {panels.map((panel) => (
          <ChartTile key={panel.title} title={panel.title} headline={panel.headline} legend={panel.series}>
            <MetricChart
              data={panel.rows}
              series={panel.series}
              range={chartRangeFor(timeRange)}
              xDomain={xDomain}
              yDomain={panel.yDomain}
              yTickFormatter={panel.yTickFormatter}
              valueFormatter={panel.valueFormatter}
              height={160}
              referenceLines={panel.referenceLines}
              syncId=""
              yAxisWidth={panel.yAxisWidth}
            />
          </ChartTile>
        ))}
      </div>
    </section>
  );
}
