'use client';

import { useEffect, useRef } from 'react';
import EmptyState from '@/components/ui/EmptyState';
import Pill from '@/components/ui/Pill';
import ScrollList from '@/components/ui/ScrollList';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { RelativeTime } from '@/components/ui/RelativeTime';
import { FOCUS_RING_INSET } from '@/components/ui/focus';
import { CheckResult, DashboardProblemMonitor } from '@/lib/types';
import Link from 'next/link';
import { problemMonitorStatus } from '@/lib/dashboard-view-model';
import { formatUptime } from '@/lib/format';
import { MONITOR_STATUS_DOT, MONITOR_STATUS_LABEL, MONITOR_STATUS_TONE, TONE_TEXT } from '@/lib/status-tones';
import { flashStyle } from '@/lib/useRowFlash';
import CheckTrace from '@/components/monitors/CheckTrace';
import { TRACE_SLOTS, LIST_HEIGHT, ROW_LINK } from '@/components/dashboard/layout';
import { ReasonText } from '@/components/dashboard/ReasonText';
import { SectionCard, SectionLoadError } from '@/components/dashboard/SectionCard';

function ProblemMonitorItem({
  monitor,
  results,
  rangeLabel,
  observe,
  flashClass,
}: {
  monitor: DashboardProblemMonitor;
  results: CheckResult[] | null;
  rangeLabel: string;
  observe: (el: Element | null, id: string) => (() => void) | undefined;
  flashClass: string;
}) {
  const ref = useRef<HTMLAnchorElement>(null);
  useEffect(() => observe(ref.current, monitor.monitor_id), [observe, monitor.monitor_id]);

  const issueCount = monitor.failure_count + monitor.error_count;
  // The monitor's current state, never inferred from its latest check: a
  // monitor failing from one location is degraded whatever its last check did.
  const status = problemMonitorStatus(monitor);
  const tone = MONITOR_STATUS_TONE[status];

  // Two lines: state, name, trace and uptime; then the state in words, the
  // short reason and how much and how recently it failed.
  return (
    <Link
      ref={ref}
      href={`/monitors/${monitor.monitor_id}`}
      className={`${ROW_LINK} ${flashClass}`}
      style={flashStyle(tone)}
    >
      <div className="flex min-w-0 items-center gap-3">
        <span className={`h-2 w-2 shrink-0 rounded-full ${MONITOR_STATUS_DOT[status]}`} aria-hidden="true" />
        <span className="min-w-0 flex-1 truncate text-sm font-medium text-white">{monitor.monitor_name}</span>
        <CheckTrace results={results} slots={TRACE_SLOTS} className="hidden w-[80px] shrink-0 sm:flex" />
        <span
          className="w-12 shrink-0 text-right text-ui font-medium tabular-nums text-slate-300"
          title={`Uptime, ${rangeLabel.toLowerCase()}`}
        >
          <span className="sr-only">Uptime </span>
          {formatUptime(monitor.uptime)}
        </span>
      </div>
      <div className="mt-0.5 flex min-w-0 items-center gap-2 pl-5 text-xs">
        <span className={`shrink-0 font-medium ${TONE_TEXT[tone]}`}>{MONITOR_STATUS_LABEL[status]}</span>
        <ReasonText message={monitor.latest_error_message} />
        <span className="ml-auto flex shrink-0 gap-2 tabular-nums text-slate-500">
          <span>{issueCount} failed</span>
          {monitor.latest_failure_at && <RelativeTime value={monitor.latest_failure_at} />}
        </span>
      </div>
      <CheckTrace results={results} slots={TRACE_SLOTS} className="mt-1.5 pl-5 sm:hidden" />
    </Link>
  );
}

export function NeedsAttentionPanel({
  monitors,
  traces,
  total,
  unhealthyCount,
  downHref,
  loading,
  loadFailed,
  onRetry,
  noMatchingMonitors,
  rangeLabel,
  observe,
  flashClass,
}: {
  /** Every problem monitor fetched, sorted; the card scrolls. */
  monitors: DashboardProblemMonitor[];
  traces: Record<string, CheckResult[]>;
  /** Every monitor needing attention (above `monitors.length` only when the list was capped). */
  total: number;
  /** Monitors down or degraded now. */
  unhealthyCount: number;
  /** The down-monitors list (it includes degraded) in the dashboard's tag scope. */
  downHref: string;
  loading: boolean;
  /** The list never loaded: show the failure, not "Nothing needs attention". */
  loadFailed: boolean;
  onRetry: () => void;
  noMatchingMonitors: boolean;
  rangeLabel: string;
  observe: (el: Element | null, id: string) => (() => void) | undefined;
  flashClass: (id: string) => string;
}) {
  const shown = monitors.length;
  return (
    <SectionCard
      title="Needs attention"
      action={
        shown > 0 ? (
          <Pill tone="neutral" size="xs" className="tabular-nums">
            {total > shown ? `${shown} of ${total}` : shown}
          </Pill>
        ) : undefined
      }
    >
      {loadFailed ? (
        <SectionLoadError onRetry={onRetry} />
      ) : loading ? (
        <TableSkeleton rows={3} columns={1} bare label="Loading monitors needing attention" />
      ) : shown > 0 ? (
        <>
          <ScrollList label="Monitors needing attention" className={`min-h-0 flex-1 ${LIST_HEIGHT}`}>
            <div className="divide-y divide-divider">
              {monitors.map((monitor) => (
                <ProblemMonitorItem
                  key={monitor.monitor_id}
                  monitor={monitor}
                  results={traces[monitor.monitor_id] ?? null}
                  rangeLabel={rangeLabel}
                  observe={observe}
                  flashClass={flashClass(monitor.monitor_id)}
                />
              ))}
            </div>
          </ScrollList>
          {/* Outside the scroll area: always reachable, whatever the list's length. */}
          {unhealthyCount > 0 && (
            <Link
              href={downHref}
              className={`shrink-0 border-t border-divider px-5 py-3 text-sm text-cyan-300 transition-colors hover:bg-well hover:text-cyan-200 ${FOCUS_RING_INSET}`}
            >
              View all {unhealthyCount} down or degraded
            </Link>
          )}
        </>
      ) : (
        <EmptyState
          variant="inline"
          headingLevel={3}
          title={noMatchingMonitors ? 'No monitors match these tags' : 'Nothing needs attention'}
          description={
            noMatchingMonitors
              ? 'Add or change tags to widen the dashboard scope.'
              : `No failed checks in the ${rangeLabel.toLowerCase()}.`
          }
        />
      )}
    </SectionCard>
  );
}
