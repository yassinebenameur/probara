'use client';

// Ad-hoc explorer over every metric series an agent pushes: series picker
// from discovery, one shared chart, agg select, per-second rates for counters.

import { useCallback, useEffect, useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, Search } from 'lucide-react';
import { MetricQueryResponse, MetricSeriesInfo } from '@/lib/types';
import { getMonitorMetricSeries, queryMonitorMetrics } from '@/lib/api';
import {
  METRIC_META,
  MetricUnitKind,
  formatMetricValue,
  metricUnitKind,
} from '@/lib/metrics';
import Callout from '@/components/ui/Callout';
import CollapsibleSection from '@/components/ui/CollapsibleSection';
import {
  ChartSeriesInput,
  MetricChart,
  RANGE_MS,
  RANGE_STEP_SECONDS,
  SERIES_COLORS,
  TimeRange,
  buildChartRows,
} from './metric-chart';

const MAX_SELECTED_SERIES = 5;

type ExplorerAgg = 'avg' | 'min' | 'max';

interface MetricExplorerProps {
  monitorId: string;
  timeRange: TimeRange;
}

function seriesLabel(series: MetricSeriesInfo): string {
  const extras = Object.entries(series.attributes)
    .map(([key, value]) => `${key}=${value}`)
    .join(', ');
  return extras || 'default';
}

export default function MetricExplorer({ monitorId, timeRange }: MetricExplorerProps) {
  const [opened, setOpened] = useState(false);
  const [seriesList, setSeriesList] = useState<MetricSeriesInfo[]>([]);
  const [seriesLoading, setSeriesLoading] = useState(false);
  const [seriesError, setSeriesError] = useState('');
  const [search, setSearch] = useState('');
  const [expandedMetrics, setExpandedMetrics] = useState<Set<string>>(new Set());
  const [selectedKeys, setSelectedKeys] = useState<string[]>([]);
  const [agg, setAgg] = useState<ExplorerAgg>('avg');
  const [queryResponse, setQueryResponse] = useState<MetricQueryResponse | null>(null);
  const [queryLoading, setQueryLoading] = useState(false);
  const [queryError, setQueryError] = useState('');

  useEffect(() => {
    if (!opened) return;
    let cancelled = false;
    setSeriesLoading(true);
    setSeriesError('');
    getMonitorMetricSeries(monitorId)
      .then((response) => {
        if (!cancelled) setSeriesList(response.items || []);
      })
      .catch((err) => {
        console.error('Failed to load metric series:', err);
        if (!cancelled) setSeriesError('Failed to load metric series');
      })
      .finally(() => {
        if (!cancelled) setSeriesLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [opened, monitorId]);

  const seriesByKey = useMemo(() => {
    const map = new Map<string, MetricSeriesInfo>();
    for (const series of seriesList) map.set(series.series_key, series);
    return map;
  }, [seriesList]);

  const selectedSeries = useMemo(
    () => selectedKeys.map((key) => seriesByKey.get(key)).filter((s): s is MetricSeriesInfo => Boolean(s)),
    [selectedKeys, seriesByKey]
  );

  const hasCounterSelected = selectedSeries.some((series) => series.metric_type === 'counter');

  const grouped = useMemo(() => {
    const query = search.trim().toLowerCase();
    const groups = new Map<string, MetricSeriesInfo[]>();
    for (const series of seriesList) {
      if (query) {
        const label = METRIC_META[series.metric_name]?.label || '';
        const haystack = `${series.series_key} ${label}`.toLowerCase();
        if (!haystack.includes(query)) continue;
      }
      const bucket = groups.get(series.metric_name);
      if (bucket) {
        bucket.push(series);
      } else {
        groups.set(series.metric_name, [series]);
      }
    }
    return Array.from(groups.entries()).sort((a, b) => a[0].localeCompare(b[0]));
  }, [seriesList, search]);

  const toggleMetricExpanded = (metricName: string) => {
    setExpandedMetrics((prev) => {
      const next = new Set(prev);
      if (next.has(metricName)) {
        next.delete(metricName);
      } else {
        next.add(metricName);
      }
      return next;
    });
  };

  const toggleSeries = (key: string) => {
    setSelectedKeys((prev) => {
      if (prev.includes(key)) return prev.filter((k) => k !== key);
      if (prev.length >= MAX_SELECTED_SERIES) return prev;
      return [...prev, key];
    });
  };

  const runQuery = useCallback(async () => {
    if (selectedSeries.length === 0) {
      setQueryResponse(null);
      return;
    }
    const now = Date.now();
    const stepSeconds = Math.min(86400, Math.max(10, RANGE_STEP_SECONDS[timeRange]));
    try {
      setQueryLoading(true);
      setQueryError('');
      const response = await queryMonitorMetrics(monitorId, {
        start: new Date(now - RANGE_MS[timeRange]).toISOString(),
        end: new Date(now).toISOString(),
        step_seconds: stepSeconds,
        queries: selectedSeries.map((series, index) => ({
          ref: `s${index}`,
          metric_name: series.metric_name,
          ...(Object.keys(series.attributes).length > 0
            ? { attribute_filters: series.attributes }
            : {}),
          agg,
          // Per-second rate is only valid (and always applied) for counters.
          ...(series.metric_type === 'counter' ? { rate: true } : {}),
        })),
      });
      setQueryResponse(response);
    } catch (err) {
      console.error('Failed to query metrics:', err);
      setQueryError('Failed to query metrics');
    } finally {
      setQueryLoading(false);
    }
  }, [monitorId, timeRange, agg, selectedSeries]);

  useEffect(() => {
    void runQuery();
  }, [runQuery]);

  const chartStepMs = useMemo(() => {
    const responseStep = queryResponse?.results?.[0]?.step_seconds;
    return (responseStep ?? Math.max(10, RANGE_STEP_SECONDS[timeRange])) * 1000;
  }, [queryResponse, timeRange]);

  const chartSeries = useMemo(() => {
    return selectedSeries.map((series, index) => {
      const result = queryResponse?.results?.find((r) => r.ref === `s${index}`);
      // Exact-attribute filters can still fan out on subset matches; take the
      // exact series when present, else the first returned.
      const data =
        result?.series?.find((s) => s.series_key === series.series_key) || result?.series?.[0];
      return {
        key: `s${index}`,
        name: `${series.metric_name}${
          Object.keys(series.attributes).length > 0 ? ` {${seriesLabel(series)}}` : ''
        }`,
        color: SERIES_COLORS[index % SERIES_COLORS.length],
        points: data?.points || [],
        rated: series.metric_type === 'counter',
        unitKind: metricUnitKind(series.metric_name, series.unit),
      };
    });
  }, [selectedSeries, queryResponse]);

  const chartRows = useMemo(() => {
    const inputs: ChartSeriesInput[] = chartSeries.map((series) => ({
      key: series.key,
      points: series.points,
    }));
    return buildChartRows(inputs, chartStepMs);
  }, [chartSeries, chartStepMs]);

  const primaryUnit: MetricUnitKind = chartSeries[0]?.unitKind ?? 'count';
  const primaryRated = chartSeries.length > 0 && chartSeries.every((series) => series.rated);
  const formatValue = (value: number) =>
    `${formatMetricValue(value, primaryUnit)}${primaryRated ? '/s' : ''}`;

  return (
    <CollapsibleSection
      title="All metrics"
      summary="Explore every series this agent pushes"
      isOpen={opened}
      onToggle={setOpened}
    >
      {seriesLoading ? (
        <p className="text-ui text-slate-500">Loading series…</p>
      ) : seriesError ? (
        <Callout tone="danger">{seriesError}</Callout>
      ) : seriesList.length === 0 ? (
        <p className="text-ui text-slate-500">No metric series recorded yet.</p>
      ) : (
        <div className="grid gap-6 2xl:grid-cols-[280px_1fr]">
          <div className="space-y-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" strokeWidth={1.75} />
              <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder="Search metrics"
                className="input input-sm !pl-8"
              />
            </div>
            <p className="text-xs text-slate-500">
              {selectedKeys.length} of up to {MAX_SELECTED_SERIES} series selected
            </p>
            <div className="max-h-80 space-y-0.5 overflow-y-auto pr-1">
              {grouped.map(([metricName, members]) => {
                const expanded = expandedMetrics.has(metricName) || Boolean(search.trim());
                const meta = METRIC_META[metricName];
                const selectedInGroup = members.filter((s) => selectedKeys.includes(s.series_key)).length;
                return (
                  <div key={metricName}>
                    <button
                      type="button"
                      onClick={() => toggleMetricExpanded(metricName)}
                      className="flex w-full items-center gap-1.5 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-well"
                    >
                      {expanded ? (
                        <ChevronDown className="h-3.5 w-3.5 shrink-0 text-slate-500" strokeWidth={1.75} />
                      ) : (
                        <ChevronRight className="h-3.5 w-3.5 shrink-0 text-slate-500" strokeWidth={1.75} />
                      )}
                      <span className="min-w-0 flex-1 truncate font-mono text-xs text-slate-200">
                        {metricName}
                      </span>
                      {meta && <span className="shrink-0 text-2xs text-slate-500">{meta.short}</span>}
                      {selectedInGroup > 0 && (
                        <span className="shrink-0 rounded-full bg-well-hover px-1.5 text-2xs tabular-nums text-slate-300">
                          {selectedInGroup}
                        </span>
                      )}
                    </button>
                    {expanded && (
                      <div className="space-y-0.5 pb-1 pl-5">
                        {members.map((series) => {
                          const selected = selectedKeys.includes(series.series_key);
                          const disabled = !selected && selectedKeys.length >= MAX_SELECTED_SERIES;
                          return (
                            <label
                              key={series.series_key}
                              className={`flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1 text-xs transition-colors ${
                                selected ? 'bg-well text-white' : 'text-slate-400 hover:bg-well hover:text-white'
                              } ${disabled ? 'cursor-not-allowed opacity-50' : ''}`}
                            >
                              <input
                                type="checkbox"
                                checked={selected}
                                disabled={disabled}
                                onChange={() => toggleSeries(series.series_key)}
                                className="h-3.5 w-3.5 accent-cyan-500"
                              />
                              <span className="min-w-0 flex-1 truncate font-mono">{seriesLabel(series)}</span>
                              <span className="shrink-0 text-2xs text-slate-500">{series.metric_type}</span>
                            </label>
                          );
                        })}
                      </div>
                    )}
                  </div>
                );
              })}
              {grouped.length === 0 && (
                <p className="px-2 py-3 text-ui text-slate-500">No metrics match the search.</p>
              )}
            </div>
          </div>

          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-4">
              <label className="flex items-center gap-2 text-xs text-slate-400">
                Aggregation
                <select
                  value={agg}
                  onChange={(e) => setAgg(e.target.value as ExplorerAgg)}
                  className="input input-sm"
                >
                  <option value="avg">avg</option>
                  <option value="min">min</option>
                  <option value="max">max</option>
                </select>
              </label>
              <label
                className="flex items-center gap-2 text-xs text-slate-400"
                title="Counters always render as per-second rates; gauges never do."
              >
                <input
                  type="checkbox"
                  checked={hasCounterSelected}
                  disabled
                  readOnly
                  className="h-3.5 w-3.5 accent-cyan-500"
                />
                Per-second rate (auto for counters)
              </label>
              {queryLoading && <span className="text-xs text-slate-500">Loading…</span>}
              {queryError && <span className="text-xs text-rose-300">{queryError}</span>}
            </div>

            {selectedSeries.length === 0 ? (
              <div className="glass-well px-4 py-6 text-center text-sm text-slate-500">
                Select series to chart them
              </div>
            ) : (
              <div className="glass-well p-4">
                <MetricChart
                  data={chartRows}
                  series={chartSeries.map(({ key, name, color }) => ({ key, name, color }))}
                  range={timeRange}
                  yDomain={[0, 'auto']}
                  yTickFormatter={formatValue}
                  valueFormatter={formatValue}
                  height={240}
                  syncId="metric-explorer"
                />
                <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-400">
                  {chartSeries.map((series) => (
                    <span key={series.key} className="flex items-center gap-1.5">
                      <span className="h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: series.color }} />
                      <span className="max-w-[320px] truncate font-mono">{series.name}</span>
                    </span>
                  ))}
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </CollapsibleSection>
  );
}
