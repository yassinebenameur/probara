'use client';

// Charts for SIP test calls (method invite): post-dial delay and answer time
// over the overview's selected range, one point per check from
// metrics_data.sip.call, and, for a call that measures its audio
// (call.media), MOS, loss and jitter per evidence basis from
// metrics_data.sip.call.media. Snapshots come from the metrics-snapshots
// endpoint, downsampled server-side to the newest check per bucket on long
// ranges and clamped to the 30-day raw retention (is_partial); they are not
// per location, so a dip at one location can fall out of a long range.
// Modelled on MongoClusterPanels and drawn with the same metric-chart
// internals; the series and threshold lines come from lib/sip-metrics.

import { ReactNode, useMemo } from 'react';
import { Monitor, MonitorAnalyticsRange, SIPMonitorConfig } from '@/lib/types';
import { formatJitter, formatLatency, formatLossPct, formatMOS } from '@/lib/format';
import { pickSnapshots, snapshotStepMs } from '@/lib/metric-snapshots';
import {
  SIP_MEDIA_BASES,
  pickSIPCall,
  sipCallSeries,
  sipCallThresholdLines,
  sipMediaJudged,
  sipMediaSeries,
  sipMediaWorst,
  type SIPMediaBasis,
} from '@/lib/sip-metrics';
import { UPTIME_TONE_TEXT, lossTone, mosTone, thresholdTone } from '@/lib/status-tones';
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
  // Text colour of the headline (status-tones), for a judged figure.
  headlineClass?: string;
  rows: ChartRow[];
  series: ChartSeries[];
  referenceLines?: ReferenceThreshold[];
  yDomain: [number | string, number | string];
  format: (value: number) => string;
}

const msValue = (v: number) => formatLatency(v);

// One line per evidence basis on the call-quality panels, and how a headline
// names the basis its worst figure came from.
const BASIS_COLORS: Record<SIPMediaBasis, string> = {
  inbound: SERIES_COLORS[2],
  outbound: SERIES_COLORS[1],
  round_trip: SERIES_COLORS[3],
};
const BASIS_HEADLINE: Record<SIPMediaBasis, string> = {
  inbound: 'inbound',
  outbound: 'outbound (RTCP)',
  round_trip: 'round trip',
};

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
  const media = call?.media;
  const ringMode = call?.mode === 'ring';
  const data = useMetricsSnapshots(monitor.id, timeRange, latestResultAt, 'call metrics');

  const panels = useMemo(() => {
    const snapshots = pickSnapshots(data, pickSIPCall);
    // No check has recorded call metrics in range (or the first fetch is in
    // flight): nothing to chart, so no empty card either.
    if (snapshots.length === 0) return { timing: [], quality: [] };

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
          : latest.teardown === 'no_provisional'
            ? 'no response on the last call'
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
      yDomain: [0, 'auto'],
      format: msValue,
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
        yDomain: [0, 'auto'],
        format: msValue,
      });
    }

    // Call quality: one line per basis the calls measured well enough to
    // estimate, the thresholds of call.media as reference lines.
    const quality: PanelSpec[] = [];
    const mediaSeries = sipMediaSeries(snapshots);
    const qualityPanel = (
      title: string,
      points: Record<SIPMediaBasis, [number, number][]>,
      headline: string,
      headlineClass: string | undefined,
      lines: ReferenceThreshold[],
      yDomain: PanelSpec['yDomain'],
      format: (value: number) => string
    ) => {
      const bases = SIP_MEDIA_BASES.filter(({ key }) => points[key].length > 0);
      if (bases.length === 0) return;
      quality.push({
        title,
        headline,
        headlineClass,
        rows: buildChartRows(
          bases.map(({ key }) => ({ key, points: points[key] })),
          stepMs
        ),
        series: bases.map(({ key, name }) => ({ key, name, color: BASIS_COLORS[key] })),
        referenceLines: lines.length ? lines : undefined,
        yDomain,
        format,
      });
    };
    const notMeasured =
      latest.media && !sipMediaJudged(latest.media) ? 'not judged on the last call' : 'not measured on the last call';
    const worst = latest.media ? sipMediaWorst(latest.media) : {};
    const headline = (figure: { value: number; basis: SIPMediaBasis } | undefined, format: (v: number) => string) =>
      figure ? `${format(figure.value)} ${BASIS_HEADLINE[figure.basis]}` : notMeasured;
    qualityPanel(
      'MOS (estimate)',
      mediaSeries.mos,
      headline(worst.mos, formatMOS),
      worst.mos ? UPTIME_TONE_TEXT[mosTone(worst.mos.value, media?.warn_mos, media?.min_mos)] : undefined,
      sipCallThresholdLines(media?.warn_mos, media?.min_mos, formatMOS),
      [1, 4.5],
      formatMOS
    );
    qualityPanel(
      'Loss',
      mediaSeries.loss,
      headline(worst.loss, formatLossPct),
      worst.loss ? UPTIME_TONE_TEXT[lossTone(worst.loss.value, media?.warn_loss_pct, media?.max_loss_pct)] : undefined,
      sipCallThresholdLines(media?.warn_loss_pct, media?.max_loss_pct, formatLossPct),
      [0, 'auto'],
      formatLossPct
    );
    qualityPanel(
      'Jitter',
      mediaSeries.jitter,
      headline(worst.jitter, formatJitter),
      // Jitter has no default band: toned only against the monitor's own.
      worst.jitter && (media?.warn_jitter_ms !== undefined || media?.max_jitter_ms !== undefined)
        ? UPTIME_TONE_TEXT[thresholdTone(worst.jitter.value, media?.warn_jitter_ms, media?.max_jitter_ms)]
        : undefined,
      sipCallThresholdLines(media?.warn_jitter_ms, media?.max_jitter_ms, formatJitter),
      [0, 'auto'],
      formatJitter
    );
    return { timing: specs, quality };
  }, [data, monitor.interval_seconds, ringMode, call, media]);

  if (panels.timing.length === 0) return null;

  // Snapshots are the newest check per bucket on long ranges, from whichever
  // location ran it: say so, since a dip at one location can fall between.
  const sampling =
    (data?.bucket_seconds ?? 0) > 0 ? 'Latest check per bucket, any location' : 'One point per check, any location';

  return (
    <>
      <PanelCard
        title="Call timing"
        info={
          <>
            One point per test call. Post-dial delay runs from the INVITE that drew it (the authenticated one when the
            server challenged) to the first ringing response, or to the answer when none came; answer time runs from
            that INVITE to the 200. When a call was challenged, a second line measures from the first INVITE, so it
            includes the digest round trip. Calls that stopped earlier (no ringing, no answer, no response) leave a gap.
            Longer ranges keep one call per time bucket.
          </>
        }
        partial={data?.is_partial}
        panels={panels.timing}
        timeRange={timeRange}
      />
      {panels.quality.length > 0 && (
        <PanelCard
          title="Call quality"
          subtitle={sampling}
          info={
            <>
              One line per evidence basis: inbound is the far end&apos;s audio as the probe received it, outbound (RTCP)
              the far end&apos;s reports on the probe&apos;s audio, round trip (echo) the probe&apos;s own audio echoed
              back. MOS is an ITU-T G.107 E-model estimate (MOS-CQE), not a listening test; loss is the loss the
              thresholds judge (effective loss, network loss for RTCP) and jitter the mean RFC 3550 jitter. A call adds
              a point to a basis only when it measured enough of it to judge, as the thresholds require, so short or
              cut-off calls, and calls a busy worker could not time, leave a gap. The points are the monitor&apos;s checks from any location, the newest per
              time bucket on longer ranges, so a dip at one location can fall between them.
            </>
          }
          partial={data?.is_partial}
          panels={panels.quality}
          timeRange={timeRange}
        />
      )}
    </>
  );
}

// One card of panels: a title with its info tip (and optional subtitle), the
// raw-retention note, and a grid of charts with a legend when a panel draws
// more than one line.
function PanelCard({
  title,
  subtitle,
  info,
  partial,
  panels,
  timeRange,
}: {
  title: string;
  subtitle?: string;
  info: ReactNode;
  partial?: boolean;
  panels: PanelSpec[];
  timeRange: MonitorAnalyticsRange;
}) {
  return (
    <div className="glass p-5">
      <div className={`${subtitle ? 'mb-1' : 'mb-4'} flex items-center gap-1.5`}>
        <h3 className="text-title font-semibold text-white">{title}</h3>
        <InfoTip inLabel ariaLabel={`About ${title.toLowerCase()}`}>
          {info}
        </InfoTip>
        {partial && <span className="ml-auto text-xs text-slate-400">Last 30 days — raw check retention</span>}
      </div>
      {subtitle && <p className="mb-4 text-xs text-slate-500">{subtitle}</p>}
      <div className="grid gap-4 md:grid-cols-2">
        {panels.map((panel) => (
          <div key={panel.title} className="glass-well p-4">
            <div className="mb-3 flex items-center justify-between gap-3">
              <span className="text-ui font-medium text-slate-200">{panel.title}</span>
              <span className={`text-xs tabular-nums ${panel.headlineClass ?? 'text-slate-400'}`}>{panel.headline}</span>
            </div>
            <MetricChart
              data={panel.rows}
              series={panel.series}
              range={chartRangeFor(timeRange)}
              yDomain={panel.yDomain}
              yTickFormatter={panel.format}
              valueFormatter={panel.format}
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
