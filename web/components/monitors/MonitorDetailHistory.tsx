'use client';

import { Check } from 'lucide-react';
import { CheckResult, MonitorAnalyticsResponse } from '@/lib/types';
import { countOperationalResults, getOperationalResults, shortFailureReason } from '@/lib/monitor-utils';
import { formatDateTime, formatDateTimeFull, formatTimestamp, formatUptime, timezoneLabel } from '@/lib/format';
import { checkResultLabel, checkResultTone, failedChecksTone, UPTIME_TONE_TEXT, uptimeTone } from '@/lib/status-tones';
import Pill from '@/components/ui/Pill';
import EmptyState from '@/components/ui/EmptyState';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { bucketFailedChecks, Stat, StatStrip } from './MonitorAnalyticsOverview';

interface MonitorDetailHistoryProps {
  /** Raw results of the last 7 days. */
  results: CheckResult[];
  /**
   * The 7-day analytics, so availability is the same interval-based number
   * the Overview shows for 7d rather than a client-side count.
   */
  analytics?: MonitorAnalyticsResponse | null;
  /** Strictest SLA target covering the monitor. */
  slaTarget: number;
  timeoutSeconds: number;
  loading?: boolean;
}

const FAILURE_LIMIT = 15;

export default function MonitorDetailHistory({
  results,
  analytics,
  slaTarget,
  timeoutSeconds,
  loading = false,
}: MonitorDetailHistoryProps) {
  if (loading && results.length === 0) {
    return <TableSkeleton rows={4} columns={3} label="Loading history" />;
  }

  const operationalResults = getOperationalResults(results);
  const failures = operationalResults.filter((r) => r.status === 'failure' || r.status === 'error');
  const summary = analytics?.summary;
  const availability = summary?.has_data ? (summary.availability_pct ?? summary.sla_pct) : null;
  // Counts come from the 7-day analytics buckets, the same source as the
  // availability beside them and the Overview at 7d: the raw results
  // endpoint is row-capped (multi-location monitors hit the cap well
  // inside 7 days), so it only feeds the list of recent failures.
  const series = analytics?.uptime_series ?? [];
  const fromAnalytics = series.length > 0;
  const totalChecks = fromAnalytics
    ? series.reduce((sum, p) => sum + p.total_checks, 0)
    : countOperationalResults(results);
  const failedChecks = fromAnalytics
    ? series.reduce((sum, p) => sum + bucketFailedChecks(p.total_checks, p.has_data ? p.uptime_pct : null), 0)
    : failures.length;
  // The same set as the list: platform results (expired jobs) never ran, so
  // they are not failed checks.
  const recentFailures = failures.slice(0, FAILURE_LIMIT);
  // Raw rows that stop short of the window (cap or retention) are stated,
  // never passed off as the whole week.
  const oldestRaw = results.length > 0 ? results[results.length - 1].created_at : null;
  const windowStart = series[0]?.bucket_start ?? null;
  const rawStartsLate =
    oldestRaw !== null && windowStart !== null && Date.parse(oldestRaw) - Date.parse(windowStart) > 6 * 3600 * 1000;
  const listCaption = [
    failures.length > FAILURE_LIMIT ? `The latest ${FAILURE_LIMIT}, newest first.` : 'Newest first.',
    rawStartsLate && oldestRaw ? `Check records go back to ${formatDateTime(oldestRaw)}.` : '',
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <div className="space-y-6">
      <StatStrip>
        <Stat
          label="Availability, last 7 days"
          value={formatUptime(availability)}
          valueClassName={availability === null ? 'text-slate-500' : UPTIME_TONE_TEXT[uptimeTone(availability, slaTarget)]}
        />
        <Stat label="Checks" value={totalChecks.toLocaleString()} />
        <Stat
          label="Failed checks"
          value={failedChecks.toLocaleString()}
          valueClassName={failedChecks === 0 ? 'text-white' : UPTIME_TONE_TEXT[failedChecksTone(failedChecks, totalChecks)]}
        />
      </StatStrip>

      <section className="glass overflow-hidden">
        <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1 px-5 pb-3 pt-4">
          <div>
            <h3 className="text-title font-semibold text-white">Failed checks, last 7 days</h3>
            <p className="mt-0.5 text-xs text-slate-500">
              {listCaption}
            </p>
          </div>
          {recentFailures.length > 0 && <span className="text-xs text-slate-500">Times in {timezoneLabel()}</span>}
        </div>

        {recentFailures.length > 0 ? (
          <ul className="divide-y divide-divider border-t border-divider">
            {recentFailures.map((failure) => {
              const reason = shortFailureReason(failure.error_message, {
                httpStatus: failure.http_status,
                timeoutSeconds,
              });
              return (
                <li key={failure.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-5 py-2.5">
                  <span className="font-mono text-xs text-slate-300" title={formatDateTimeFull(failure.created_at)}>
                    {formatTimestamp(failure.created_at)}
                  </span>
                  <Pill tone={checkResultTone(failure.status)} size="xs">
                    {checkResultLabel(failure.status)}
                  </Pill>
                  {failure.location_name && (
                    <Pill tone="neutral" size="xs">{failure.location_name}</Pill>
                  )}
                  {reason && (
                    <span
                      className="min-w-0 basis-full truncate text-ui text-slate-300 sm:basis-0 sm:flex-1"
                      title={failure.error_message || reason}
                    >
                      {reason}
                    </span>
                  )}
                </li>
              );
            })}
          </ul>
        ) : (
          <div className="border-t border-divider">
            <EmptyState
              variant="inline"
              icon={<Check className="h-5 w-5 text-emerald-400" strokeWidth={1.75} aria-hidden="true" />}
              title={rawStartsLate && oldestRaw ? `No failed checks since ${formatDateTime(oldestRaw)}.` : 'No failed checks in the last 7 days.'}
            />
          </div>
        )}
      </section>
    </div>
  );
}
