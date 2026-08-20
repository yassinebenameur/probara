'use client';

// Charts for the MongoDB cluster checks (clusterMonitor role). Each check
// result carries one snapshot in metrics_data.mongodb; gauges chart directly,
// cumulative counters (opcounters, network bytes) chart as per-second rates
// between consecutive checks. Reuses the metric-store chart internals so the
// panels match the agent dashboard.

import { useMemo } from 'react';
import { CheckResult, Monitor, MongoDBMetrics, MongoDBMonitorConfig } from '@/lib/types';
import { formatBytes } from '@/lib/metrics';
import InfoTip from '@/components/ui/InfoTip';
import {
  MetricChart,
  buildChartRows,
  SERIES_COLORS,
  type ChartRow,
  type ChartSeries,
  type ReferenceThreshold,
} from './metric-chart';

interface Snapshot {
  ts: number;
  m: MongoDBMetrics;
}

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
const countValue = (v: number) => `${Math.round(v).toLocaleString()}`;
const bytesTick = (v: number) => formatBytes(v);
const rateValue = (v: number) => `${formatBytes(v)}/s`;
const opsValue = (v: number) => `${v >= 10 ? Math.round(v) : v.toFixed(1)}/s`;

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

export default function MongoClusterPanels({ monitor, results }: { monitor: Monitor; results: CheckResult[] }) {
  const config = (monitor.config || {}) as MongoDBMonitorConfig;

  const { panels, unauthorized } = useMemo(() => {
    // Results arrive newest-first; charts want time ascending.
    const snapshots: Snapshot[] = [];
    for (let i = results.length - 1; i >= 0; i--) {
      const r = results[i];
      const m = (r.metrics_data as { mongodb?: MongoDBMetrics } | undefined)?.mongodb;
      if (!m) continue;
      const ts = Date.parse(r.created_at);
      if (Number.isFinite(ts)) snapshots.push({ ts, m });
    }

    const latest = snapshots[snapshots.length - 1]?.m;
    const isUnauthorized = Boolean(latest?.unavailable?.some((u) => u.reason === 'unauthorized'));

    // Snap rows to the observed check cadence (median inter-snapshot delta),
    // not the configured interval: scheduler ticks land with jitter (e.g.
    // 35s on a 30s interval), and a too-fine grid turns that jitter into
    // false line breaks. A genuinely missed check still leaves a >1-bucket
    // hole, so real gaps keep breaking the line.
    const deltas = snapshots
      .slice(1)
      .map((s, i) => s.ts - snapshots[i].ts)
      .sort((a, b) => a - b);
    const medianDelta = deltas[Math.floor(deltas.length / 2)] ?? 0;
    // The 1.25 pad absorbs fractional drift (35s points on a 35s grid would
    // still skip a bucket now and then); two checks in one bucket just
    // overwrite, which is invisible, while a missed check still leaves a hole.
    const stepMs = Math.max(monitor.interval_seconds * 1000, Math.ceil(medianDelta * 1.25), 1000);
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
          { key: 'commands', name: 'Commands', color: SERIES_COLORS[5] },
        ],
        yTickFormatter: opsValue,
        valueFormatter: opsValue,
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
  }, [results, monitor.interval_seconds, config.warn_replication_lag_seconds, config.max_replication_lag_seconds]);

  if (panels.length === 0 && !unauthorized) return null;

  return (
    <div className="rounded-xl border border-white/[0.06] bg-slate-900/40 p-4">
      <div className="mb-4 flex items-center gap-1.5">
        <h3 className="text-sm font-medium text-white">Cluster metrics</h3>
        <InfoTip inLabel ariaLabel="About cluster metrics">
          One snapshot per check from the enabled cluster checks (serverStatus / replSetGetStatus, read with the
          clusterMonitor role). Operation and network counters are cumulative on the server and shown here as
          per-second rates between checks.
        </InfoTip>
      </div>
      {unauthorized && (
        <p className="mb-4 rounded-lg border border-amber-500/30 bg-amber-500/[0.07] px-3 py-2 text-xs text-amber-300">
          Cluster checks are enabled but skipped — grant the monitoring user the clusterMonitor role.
        </p>
      )}
      <div className="grid gap-6 md:grid-cols-2">
        {panels.map((panel) => (
          <div key={panel.title}>
            <div className="mb-2 flex items-center justify-between">
              <span className="text-xs text-slate-500">{panel.title}</span>
              <span className="font-mono text-xs text-slate-400">{panel.headline}</span>
            </div>
            <div className="rounded-lg border border-white/[0.06] bg-slate-950/50 p-3">
              <MetricChart
                data={panel.rows}
                series={panel.series}
                range="6h"
                yDomain={panel.yDomain}
                yTickFormatter={panel.yTickFormatter}
                valueFormatter={panel.valueFormatter}
                height={150}
                referenceLines={panel.referenceLines}
                syncId=""
                yAxisWidth={panel.yAxisWidth}
              />
            </div>
            {panel.series.length > 1 && (
              <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-slate-500">
                {panel.series.map((s) => (
                  <span key={s.key} className="flex items-center gap-1.5">
                    <span className="h-1.5 w-1.5 rounded-full" style={{ backgroundColor: s.color }} />
                    {s.name}
                  </span>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
