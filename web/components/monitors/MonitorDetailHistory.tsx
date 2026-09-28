'use client';

import { Check } from 'lucide-react';
import { CheckResult } from '@/lib/types';
import { calculateUptime, countOperationalResults, getOperationalResults } from '@/lib/monitor-utils';
import Pill from '@/components/ui/Pill';

interface MonitorDetailHistoryProps {
  results: CheckResult[];
  loading?: boolean;
}

export default function MonitorDetailHistory({
  results,
  loading = false,
}: MonitorDetailHistoryProps) {
  if (loading) {
    return (
      <div className="flex items-center justify-center py-12 text-sm text-slate-500">
        Loading history...
      </div>
    );
  }

  const operationalResults = getOperationalResults(results);
  const failures = operationalResults.filter(r => r.status === 'failure' || r.status === 'error');
  const uptime = calculateUptime(results);
  const totalChecks = countOperationalResults(results);
  const failedChecks = failures.length;
  const recentFailures = results.filter(r => r.status === 'failure' || r.status === 'error').slice(0, 15);

  const formatDate = (dateStr: string) => {
    return new Date(dateStr).toISOString().replace('T', ' ').slice(0, 19);
  };

  return (
    <div className="space-y-6">
      <div className="glass grid gap-4 p-5 sm:grid-cols-3">
        <div>
          <p className="text-xs text-slate-400">Uptime (7 days)</p>
          <p className={`mt-1 text-2xl font-semibold tabular-nums ${uptime >= 99 ? 'text-white' : 'text-amber-300'}`}>
            {uptime.toFixed(3)}%
          </p>
        </div>
        <div>
          <p className="text-xs text-slate-400">Total checks</p>
          <p className="mt-1 text-2xl font-semibold tabular-nums text-white">{totalChecks}</p>
        </div>
        <div>
          <p className="text-xs text-slate-400">Failed checks</p>
          <p className={`mt-1 text-2xl font-semibold tabular-nums ${failedChecks === 0 ? 'text-white' : 'text-rose-300'}`}>
            {failedChecks}
          </p>
        </div>
      </div>

      <div className="glass overflow-hidden">
        <div className="px-5 py-4">
          <h3 className="text-[15px] font-semibold text-white">Incident history</h3>
          <p className="mt-0.5 text-xs text-slate-500">Recent failures and errors</p>
        </div>

        {recentFailures.length > 0 ? (
          <div className="divide-y divide-divider">
            {recentFailures.map((failure) => (
              <div key={failure.id} className="px-5 py-3.5">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-mono text-[13px] text-slate-300">{formatDate(failure.created_at)}</span>
                  <Pill tone={failure.status === 'failure' ? 'danger' : 'warning'} size="xs">
                    {failure.status === 'failure' ? 'Failed' : 'Error'}
                  </Pill>
                  {failure.http_status && (
                    <span className="text-xs text-slate-500">HTTP {failure.http_status}</span>
                  )}
                  {failure.location_name && (
                    <Pill tone="neutral" size="xs">{failure.location_name}</Pill>
                  )}
                </div>
                {failure.error_message && (
                  <p className="mt-1 truncate text-[13px] text-slate-400">{failure.error_message}</p>
                )}
              </div>
            ))}
          </div>
        ) : (
          <div className="px-5 pb-12 pt-6 text-center">
            <span className="mb-3 inline-flex h-11 w-11 items-center justify-center rounded-full bg-emerald-500/[0.13] text-emerald-400">
              <Check className="h-5 w-5" strokeWidth={1.75} aria-hidden="true" />
            </span>
            <p className="text-sm font-medium text-white">No failures recorded</p>
            <p className="mt-1 text-xs text-slate-500">This monitor is performing well!</p>
          </div>
        )}
      </div>
    </div>
  );
}
