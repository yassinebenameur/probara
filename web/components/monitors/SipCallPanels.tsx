'use client';

// Charts for SIP test calls (method invite): post-dial delay and answer time
// over the overview's selected range, one point per check from
// metrics_data.sip.call. Snapshots come from the metrics-snapshots endpoint,
// downsampled server-side to the newest check per bucket on long ranges and
// clamped to the 30-day raw retention (is_partial). Modelled on
// MongoClusterPanels and drawn with the same metric-chart internals; the
// series and threshold lines come from lib/sip-metrics.

import { useMemo } from 'react';
import { Monitor, MonitorAnalyticsRange, SIPMonitorConfig } from '@/lib/types';
import { formatLatency } from '@/lib/format';
import { pickSnapshots, snapshotStepMs } from '@/lib/metric-snapshots';
import { pickSIPCall, sipCallSeries, sipCallThresholdLines } from '@/lib/sip-metrics';
import { useMetricsSnapshots } from '@/lib/useMetricsSnapshots';
import InfoTip from '@/components/ui/InfoTip';
import {
  MetricChart,
  buildChartRows,
  chartRangeFor,
  SERIES_COLORS,
  type ChartRow,
  type ChartSeries,
  type ReferenceThreshold,
} from './metric-chart';

interface PanelSpec {
  title: string;
  headline: string;
  rows: ChartRow[];
  series: ChartSeries[];
  referenceLines?: ReferenceThreshold[];
}

const msValue = (v: number) => formatLatency(v);

export default function SipCallPanels({
  monitor,
  timeRange,
  latestResultAt,
}: {
  monitor: Monitor;
  timeRange: MonitorAnalyticsRange;
  // created_at of the newest known check; a change triggers a refetch.
  latestResultAt?: string;
}) {
  const call = (monitor.config as SIPMonitorConfig | undefined)?.call;
  const ringMode = call?.mode === 'ring';
  const data = useMetricsSnapshots(monitor.id, timeRange, latestResultAt, 'call metrics');

  const panels = useMemo(() => {
    const snapshots = pickSnapshots(data, pickSIPCall);
    // No check has recorded call metrics in range (or the first fetch is in
    // flight): nothing to chart, so no empty card either.
    if (snapshots.length === 0) return [];

    const stepMs = snapshotStepMs(
      snapshots.map((snap) => snap.ts),
      monitor.interval_seconds,
      data?.bucket_seconds ?? 0
    );
    const series = sipCallSeries(snapshots);
    const latest = snapshots[snapshots.length - 1].m;
    const specs: PanelSpec[] = [];

    const pddLines = sipCallThresholdLines(call?.warn_pdd_ms, call?.max_pdd_ms);
    specs.push({
      title: 'Post-dial delay',
      headline:
        latest.pdd_ms !== undefined
          ? `${formatLatency(latest.pdd_ms)}${latest.pdd_source ? ` (${latest.pdd_source})` : ''}`
          : 'no ringing on the last call',
      rows: buildChartRows(
        [
          { key: 'pdd', points: series.pdd },
          { key: 'setup', points: series.setupPdd },
        ],
        stepMs
      ),
      series: [
        { key: 'pdd', name: 'PDD', color: SERIES_COLORS[0] },
        ...(series.setupPdd.length > 0 ? [{ key: 'setup', name: 'From first INVITE', color: SERIES_COLORS[2] }] : []),
      ],
      referenceLines: pddLines.length ? pddLines : undefined,
    });

    // Ring mode never answers on purpose; its panel appears only when some
    // call was answered anyway (billable, and flagged on that check).
    if (!ringMode || series.answer.length > 0) {
      const answerLines = ringMode ? [] : sipCallThresholdLines(call?.warn_answer_ms, call?.max_answer_ms);
      specs.push({
        title: 'Answer time',
        headline:
          latest.answer_ms !== undefined ? formatLatency(latest.answer_ms) : 'not answered on the last call',
        rows: buildChartRows(
          [
            { key: 'answer', points: series.answer },
            { key: 'setup', points: series.setup },
          ],
          stepMs
        ),
        series: [
          { key: 'answer', name: 'Answer', color: SERIES_COLORS[3] },
          ...(series.setup.length > 0 ? [{ key: 'setup', name: 'From first INVITE', color: SERIES_COLORS[2] }] : []),
        ],
        referenceLines: answerLines.length ? answerLines : undefined,
      });
    }
    return specs;
  }, [
    data,
    monitor.interval_seconds,
    ringMode,
    call?.warn_pdd_ms,
    call?.max_pdd_ms,
    call?.warn_answer_ms,
    call?.max_answer_ms,
  ]);

  if (panels.length === 0) return null;

  return (
    <div className="glass p-5">
      <div className="mb-4 flex items-center gap-1.5">
        <h3 className="text-title font-semibold text-white">Call timing</h3>
        <InfoTip inLabel ariaLabel="About call timing">
          One point per test call. Post-dial delay runs from the INVITE that drew it (the authenticated one when the
          server challenged) to the first ringing response, or to the answer when none came; answer time runs from that
          INVITE to the 200. When a call was challenged, a second line measures from the first INVITE, so it includes
          the digest round trip. Calls that stopped earlier (no ringing, no answer, no response) leave a gap. Longer
          ranges keep one call per time bucket.
        </InfoTip>
        {data?.is_partial && (
          <span className="ml-auto text-xs text-slate-400">Last 30 days — raw check retention</span>
        )}
      </div>
      <div className="grid gap-4 md:grid-cols-2">
        {panels.map((panel) => (
          <div key={panel.title} className="glass-well p-4">
            <div className="mb-3 flex items-center justify-between gap-3">
              <span className="text-ui font-medium text-slate-200">{panel.title}</span>
              <span className="text-xs tabular-nums text-slate-400">{panel.headline}</span>
            </div>
            <MetricChart
              data={panel.rows}
              series={panel.series}
              range={chartRangeFor(timeRange)}
              yDomain={[0, 'auto']}
              yTickFormatter={msValue}
              valueFormatter={msValue}
              height={150}
              referenceLines={panel.referenceLines}
              syncId=""
              yAxisWidth={64}
            />
            {panel.series.length > 1 && (
              <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs text-slate-400">
                {panel.series.map((s) => (
                  <span key={s.key} className="flex items-center gap-1.5">
                    <span className="h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: s.color }} />
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
