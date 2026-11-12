'use client';

import Pill from '@/components/ui/Pill';
import { RelativeTime } from '@/components/ui/RelativeTime';
import CopyableTarget from '@/components/ui/CopyableTarget';
import CheckTrace from '@/components/monitors/CheckTrace';
import TagPill from '@/components/monitors/TagPill';
import { Monitor, CheckResult, HTTPDisplayValue } from '@/lib/types';
import { calculateUptime, displayValueLabel, formatInterval, getOperationalResults, monitorTarget, monitorTargetLabel, MonitorDisplayStatus, calculateLatencyStats } from '@/lib/monitor-utils';
import { formatDateTime, formatLatency, formatUptime } from '@/lib/format';
import { MONITOR_STATUS_DOT, MONITOR_STATUS_LABEL } from '@/lib/status-tones';
import { RECENT_CHECKS } from '@/components/monitors/list/constants';
import { useRowLayout } from '@/components/monitors/list/row-layout';

// Paused is a hollow ring so it never reads as "no data" (solid grey).
// `name` labels a dot that stands alone for a monitor (a group's member dots).
export function StatusDot({ status, name }: { status: MonitorDisplayStatus; name?: string }) {
  const label = name ? `${name}: ${MONITOR_STATUS_LABEL[status]}` : MONITOR_STATUS_LABEL[status];
  const fill =
    status === 'paused' ? 'ring-[1.5px] ring-inset ring-slate-400' : MONITOR_STATUS_DOT[status];
  return (
    <span
      role="img"
      aria-label={label}
      title={label}
      className={`h-2 w-2 flex-shrink-0 rounded-full transition-colors duration-slow ${fill}`}
    />
  );
}

/** Paused and maintenance are states the operator chose; say so on the row. */
export function StatePill({ monitor, status }: { monitor: Monitor; status: MonitorDisplayStatus }) {
  if (status === 'paused') {
    return <Pill tone="neutral" size="xs">Paused</Pill>;
  }
  if (status === 'maintenance') {
    return (
      <span
        className="inline-flex flex-shrink-0"
        title={
          monitor.maintenance_until
            ? `Alerts suppressed until ${formatDateTime(monitor.maintenance_until)}`
            : 'Alerts suppressed during maintenance'
        }
      >
        <Pill tone="info" size="xs">Maintenance</Pill>
      </span>
    );
  }
  return null;
}

/**
 * Second line of a row: the type tag, then the target. Below md the target is
 * plain text (the row opens the detail page, which has copy); from md it is
 * a copy chip whose actions appear on hover. Targetless types show their
 * interval so every row keeps the same height.
 */
export function TargetLine({ monitor }: { monitor: Monitor }) {
  const target = monitorTarget(monitor);
  if (!target) {
    return <span className="truncate text-xs text-slate-500">{formatInterval(monitor.interval_seconds)}</span>;
  }
  return (
    <>
      <span className="min-w-0 truncate font-mono text-xs text-slate-500 md:hidden">{target}</span>
      <span className="hidden min-w-0 md:flex">
        {/* Out of the tab order: copy is also on the preview and detail
            pages, and a row should cost one Tab stop, not three. */}
        <CopyableTarget
          value={target}
          label={monitorTargetLabel(monitor.type)}
          size="sm"
          revealOnHover
          focusable={false}
          textClassName="text-slate-500"
        />
      </span>
    </>
  );
}

/** The target in its own column (very wide layouts). Groups have none. */
export function TargetCell({ monitor }: { monitor?: Monitor }) {
  const layout = useRowLayout();
  return (
    <div role="cell" className={`${layout.target} min-w-0 items-center`}>
      {monitor && <TargetLine monitor={monitor} />}
    </div>
  );
}

export function TagList({ tags, max }: { tags?: string[]; max: number }) {
  if (!tags || tags.length === 0) return null;
  return (
    <span className="hidden flex-shrink-0 items-center gap-1 sm:flex">
      {tags.slice(0, max).map((tag) => (
        <TagPill key={tag} tag={tag} size="xs" />
      ))}
      {tags.length > max && <span className="text-2xs text-slate-500">+{tags.length - max}</span>}
    </span>
  );
}

/**
 * Trace, success share, latency and last check: the wide columns, plus the
 * compact cell (short trace + success) that replaces them on narrow screens.
 * A paused monitor's figures are history, so they are muted rather than
 * shown as live health (a green trace weeks old reads as "up").
 */
export function RowMetrics({
  results,
  showLatency = true,
  paused = false,
}: {
  results: CheckResult[];
  showLatency?: boolean;
  paused?: boolean;
}) {
  const layout = useRowLayout();
  const operational = getOperationalResults(results);
  const passed = operational.filter((r) => r.status === 'success').length;
  const success = operational.length > 0 ? formatUptime(calculateUptime(results)) : '—';
  const successTitle =
    operational.length > 0 ? `${passed} of the last ${operational.length} checks passed` : 'No checks yet';
  const latency = calculateLatencyStats(results);
  const latest = results[0];

  return (
    <>
      <div role="cell" className={`${layout.narrow} flex-col items-end gap-1`}>
        <CheckTrace results={results} slots={10} size="sm" muted={paused} />
        <span className={`text-2xs tabular-nums ${paused ? 'text-slate-500' : 'text-slate-400'}`} title={successTitle}>{success}</span>
      </div>
      <div role="cell" className={`${layout.wide} min-w-0 justify-end`}>
        <CheckTrace results={results} slots={RECENT_CHECKS} muted={paused} className="flex-none" />
      </div>
      <div
        role="cell"
        className={`${layout.wide} justify-end text-xs tabular-nums ${paused ? 'text-slate-500' : 'text-slate-300'}`}
        title={successTitle}
      >
        {success}
      </div>
      <div role="cell" className={`${layout.full} justify-end text-xs tabular-nums text-slate-400`}>
        {showLatency && latency.samples > 0 ? formatLatency(latency.median) : '—'}
      </div>
      <div role="cell" className={`${layout.full} justify-end whitespace-nowrap text-xs tabular-nums text-slate-500`}>
        <RelativeTime value={latest?.created_at} fallback="Never" />
      </div>
    </>
  );
}

// Values the worker read from the latest JSON response (HTTP display_fields).
// Neutral and squared-off on purpose, so they never read as tags. Shown at
// every width (the list is the only place they appear outside the preview),
// shrinking with the name on phones.
export function DisplayValueChips({ values }: { values: HTTPDisplayValue[] }) {
  if (values.length === 0) return null;
  return (
    <span className="flex min-w-0 items-center gap-1 overflow-hidden">
      {values.map((v) => (
        <span
          key={v.path}
          className="inline-flex min-w-0 max-w-[120px] flex-shrink-0 items-center sm:max-w-[180px] gap-1 rounded-md bg-well-hover px-1.5 py-0.5 font-mono text-2xs leading-none text-slate-300"
          title={`${v.path} = ${v.value}`}
        >
          {/* slate-400, not the caption step: the chip's well-hover sits on a
              hovered or checked row, a stacked fill slate-500 can't clear. */}
          {displayValueLabel(v) && <span className="shrink-0 text-slate-400">{displayValueLabel(v)}</span>}
          <span className="truncate">{v.value}</span>
        </span>
      ))}
    </span>
  );
}
