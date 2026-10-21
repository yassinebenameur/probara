'use client';

import { ReactNode } from 'react';
import Link from 'next/link';
import type { Monitor } from '@/lib/types';
import { formatDate, formatDateTimeFull, formatDuration, pluralize } from '@/lib/format';
import { DEFAULT_MONITOR_SORT, formatMonitorListParams } from '@/lib/monitor-list';
import Panel from '@/components/ui/Panel';
import { useNow } from '@/lib/useNow';
import { FOCUS_RING } from '@/components/ui/focus';
import TagPill from './TagPill';

/**
 * Label/value line in the detail page's side panels (Details, Server).
 * Warnings and notes wrap instead of truncating so the whole message stays
 * readable.
 */
export function DetailRow({
  label,
  tone = 'default',
  children,
}: {
  label: string;
  tone?: 'default' | 'warning' | 'note';
  children: ReactNode;
}) {
  const labelClass = tone === 'warning' ? 'text-amber-300' : 'text-slate-400';
  const valueClass =
    tone === 'warning'
      ? 'text-right text-amber-300'
      : tone === 'note'
        ? 'text-right text-slate-400'
        : 'min-w-0 truncate text-right text-slate-200';
  return (
    <div className="flex justify-between gap-3">
      <dt className={`shrink-0 ${labelClass}`}>{label}</dt>
      <dd className={valueClass}>{children}</dd>
    </div>
  );
}

/** A side panel of DetailRows. */
export function DetailList({ children }: { children: ReactNode }) {
  return <dl className="space-y-2.5 text-sm">{children}</dl>;
}

const LINK_CLASS = `rounded text-slate-200 underline-offset-2 transition-colors hover:text-cyan-300 hover:underline ${FOCUS_RING}`;

/**
 * When the next check is due, from `now`: the scheduler's next_run_at while
 * it is ahead, else the newest check plus one interval. The page loads the
 * monitor once and polls only its results, so next_run_at goes stale within
 * one interval (and often lands already due); the newest result keeps the
 * estimate moving. Null when neither is ahead: the check is due now.
 */
export function nextCheckAt(
  nextRunAt: string | undefined,
  lastCheckAt: string | undefined,
  intervalSeconds: number,
  now: number
): number | null {
  const scheduled = nextRunAt ? Date.parse(nextRunAt) : Number.NaN;
  if (scheduled > now) return scheduled;
  const projected = lastCheckAt && intervalSeconds > 0 ? Date.parse(lastCheckAt) + intervalSeconds * 1000 : Number.NaN;
  return projected > now ? projected : null;
}

// A "next" time never reads as "ago": `in 45s`, or `due now` once passed.
function NextCheck({ monitor, lastCheckAt }: { monitor: Monitor; lastCheckAt?: string }) {
  const now = useNow('second');
  const next = nextCheckAt(monitor.next_run_at, lastCheckAt, monitor.interval_seconds, now);
  const seconds = next === null ? 0 : Math.ceil((next - now) / 1000);
  return (
    <span title={next === null ? undefined : formatDateTimeFull(next)} suppressHydrationWarning>
      {seconds > 0 ? `in ${formatDuration(seconds)}` : 'due now'}
    </span>
  );
}

// Where this monitor's alerts go, in a few words (the full explanation of an
// unrouted monitor is the callout above the tabs).
function routingSummary(monitor: Monitor): { text: ReactNode; warning: boolean } | null {
  const routing = monitor.alert_routing;
  if (!routing) return null;
  if (!routing.reachable) return { text: 'Nobody', warning: monitor.enabled };
  switch (routing.source) {
    case 'tenant_default':
      return { text: `Workspace default, ${pluralize(routing.active_channels, 'channel')}`, warning: false };
    case 'custom':
      return { text: `Custom, ${pluralize(routing.active_channels, 'channel')}`, warning: false };
    case 'group_rollup':
      return {
        text: routing.rollup_group_id ? (
          <Link href={`/monitors/${routing.rollup_group_id}`} className={LINK_CLASS}>
            Group {routing.rollup_group_name ?? ''}
          </Link>
        ) : (
          'Its group'
        ),
        warning: false,
      };
    case 'members':
      return { text: "Each member's own routing", warning: false };
    default:
      return null;
  }
}

/**
 * The monitor's standing configuration at a glance, first in the overview's
 * side column: how it alerts and where, where it runs, what it is tagged
 * with. Interval and timeout are in the page header, so they are not
 * repeated here.
 */
export default function MonitorFactsCard({
  monitor,
  passive,
  lastCheckAt,
}: {
  monitor: Monitor;
  passive: boolean;
  /** The newest check, which keeps "Next check" current between monitor reloads. */
  lastCheckAt?: string;
}) {
  const isGroup = monitor.type === 'group';
  const routing = routingSummary(monitor);
  const locations = monitor.locations ?? [];
  const tags = monitor.tags ?? [];

  return (
    <Panel title="Details">
      <DetailList>
        {isGroup ? (
          <DetailRow label="Alerts">
            {monitor.member_alert_rollup === 'group' ? 'One alert for the group' : 'Each member alerts'}
          </DetailRow>
        ) : (
          monitor.consecutive_failures_threshold > 0 && (
            <DetailRow label="Alerts after">
              {pluralize(monitor.consecutive_failures_threshold, 'failed check')}
            </DetailRow>
          )
        )}
        {routing && (
          <DetailRow label="Notifies" tone={routing.warning ? 'warning' : 'default'}>
            {routing.text}
          </DetailRow>
        )}
        {!passive && (
          <DetailRow label="Runs from">
            {locations.length > 0 ? pluralize(locations.length, 'location') : 'Default fleet'}
          </DetailRow>
        )}
        {!passive && monitor.enabled && (monitor.next_run_at || lastCheckAt) && (
          <DetailRow label="Next check">
            <NextCheck monitor={monitor} lastCheckAt={lastCheckAt} />
          </DetailRow>
        )}
        <DetailRow label="Created">
          <span title={formatDateTimeFull(monitor.created_at)}>{formatDate(monitor.created_at)}</span>
        </DetailRow>
        {tags.length > 0 && (
          <div className="flex justify-between gap-3">
            <dt className="shrink-0 pt-0.5 text-slate-400">Tags</dt>
            <dd className="flex min-w-0 flex-wrap justify-end gap-1.5">
              {tags.map((tag) => (
                <Link
                  key={tag}
                  href={`/monitors?${formatMonitorListParams({
                    status: 'all',
                    tags: [tag],
                    unroutedOnly: false,
                    sort: DEFAULT_MONITOR_SORT,
                  })}`}
                  title={`Monitors tagged ${tag}`}
                  className={`inline-flex rounded-full transition-opacity hover:opacity-80 ${FOCUS_RING}`}
                >
                  <TagPill tag={tag} size="xs" />
                </Link>
              ))}
            </dd>
          </div>
        )}
      </DetailList>
    </Panel>
  );
}
