'use client';

// The monitors list's preview of an agent host: the latest fresh readings from
// the metric store (one POST /monitors/{id}/metrics/query). Agent heartbeats
// carry no metrics_data, so check results cannot feed this; the detail page's
// AgentMetricsView has the charts.

import { useEffect, useState } from 'react';
import { MetricQueryRefResult, MetricQuerySpec, MetricRule } from '@/lib/types';
import { queryMonitorMetrics } from '@/lib/api';
import { UPTIME_TONE_VAR, utilizationTone } from '@/lib/status-tones';

// The newest value per series within the window, one bucket per series.
const GLANCE_QUERIES: MetricQuerySpec[] = [
  { ref: 'cpu', metric_name: 'system.cpu.utilization', attribute_filters: { state: 'used' }, agg: 'last' },
  { ref: 'mem', metric_name: 'system.memory.utilization', attribute_filters: { state: 'used' }, agg: 'last' },
  // No state attribute on filesystem utilization: one series per mount.
  { ref: 'fs', metric_name: 'system.filesystem.utilization', agg: 'last' },
  { ref: 'load1', metric_name: 'system.cpu.load_average.1m', agg: 'last' },
  { ref: 'procs', metric_name: 'system.processes.count', agg: 'last' },
];

interface Reading {
  value: number;
  attributes?: Record<string, string>;
}

function lastReading(result: MetricQueryRefResult | undefined, pick: 'first' | 'max' | 'sum'): Reading | null {
  let out: Reading | null = null;
  for (const series of result?.series ?? []) {
    const point = series.points[series.points.length - 1];
    if (!point) continue;
    const value = point[1];
    if (out === null) {
      out = { value, attributes: series.attributes };
      if (pick === 'first') break;
    } else if (pick === 'sum') {
      out.value += value;
    } else if (value > out.value) {
      out = { value, attributes: series.attributes };
    }
  }
  return out;
}

function Meter({
  label,
  reading,
  metricName,
  rules,
}: {
  label: string;
  reading: Reading | null;
  metricName: string;
  rules?: MetricRule[];
}) {
  const percent = reading ? Math.min(100, Math.max(0, reading.value * 100)) : 0;
  const tone = utilizationTone(reading?.value, metricName, reading?.attributes, rules);
  return (
    <div className="glass-well p-3">
      <div className="flex items-center justify-between">
        <span className="text-xs text-slate-400">{label}</span>
        <span className="text-xs font-medium tabular-nums text-white">
          {reading ? `${percent.toFixed(1)}%` : '—'}
        </span>
      </div>
      <div className="mt-2 h-1 overflow-hidden rounded-full bg-well-hover">
        <div className="h-full rounded-full" style={{ width: `${percent}%`, background: UPTIME_TONE_VAR[tone] }} />
      </div>
    </div>
  );
}

export default function AgentHostGlance({
  monitorId,
  intervalSeconds,
  metricRules,
  refreshKey,
  titleClassName,
}: {
  monitorId: string;
  intervalSeconds: number;
  metricRules?: MetricRule[];
  /** Changes when a new heartbeat lands (the latest result's timestamp). */
  refreshKey?: string;
  titleClassName: string;
}) {
  const [results, setResults] = useState<Map<string, MetricQueryRefResult> | null>(null);

  useEffect(() => {
    let cancelled = false;
    // The freshness horizon state evaluation uses (interval × 3, floor 90 s),
    // widened to 5 min: a silent host's last readings must not linger.
    const windowSeconds = Math.min(86400, Math.max(3 * (intervalSeconds > 0 ? intervalSeconds : 60), 300));
    const now = Date.now();
    queryMonitorMetrics(monitorId, {
      start: new Date(now - windowSeconds * 1000).toISOString(),
      end: new Date(now).toISOString(),
      step_seconds: windowSeconds,
      queries: GLANCE_QUERIES,
    })
      .then((response) => {
        if (cancelled) return;
        setResults(new Map(response.results.map((result) => [result.ref, result])));
      })
      .catch((err) => {
        console.error('Failed to load agent host metrics:', err);
        if (!cancelled) setResults(null);
      });
    return () => {
      cancelled = true;
    };
  }, [monitorId, intervalSeconds, refreshKey]);

  if (!results) return null;
  const cpu = lastReading(results.get('cpu'), 'first');
  const mem = lastReading(results.get('mem'), 'first');
  const fs = lastReading(results.get('fs'), 'max');
  const load1 = lastReading(results.get('load1'), 'first');
  const procs = lastReading(results.get('procs'), 'sum');
  if (!cpu && !mem && !fs && !load1) return null;

  return (
    <div>
      <h4 className={titleClassName}>System metrics</h4>
      <div className="grid grid-cols-2 gap-2">
        <Meter label="CPU" reading={cpu} metricName="system.cpu.utilization" rules={metricRules} />
        <Meter label="Memory" reading={mem} metricName="system.memory.utilization" rules={metricRules} />
        <Meter label="Fullest disk" reading={fs} metricName="system.filesystem.utilization" rules={metricRules} />
        <div className="glass-well p-3">
          <div className="flex items-center justify-between">
            <span className="text-xs text-slate-400">Load avg</span>
            <span className="text-xs font-medium tabular-nums text-white">{load1 ? load1.value.toFixed(2) : '—'}</span>
          </div>
          {procs && <p className="mt-1.5 text-2xs text-slate-500">{Math.round(procs.value)} processes</p>}
        </div>
      </div>
    </div>
  );
}
