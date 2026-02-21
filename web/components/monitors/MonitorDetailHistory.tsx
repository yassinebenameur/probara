'use client';

import { CheckResult } from '@/lib/types';
import { calculateUptime, countOperationalResults, getOperationalResults } from '@/lib/monitor-utils';

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
      <div className="flex items-center justify-center py-12">
        <div className="text-slate-500">Loading history...</div>
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
      {/* Summary Cards */}
      <div className="grid gap-4 sm:grid-cols-3">
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
          <p className="text-xs text-slate-500">Uptime (30 days)</p>
          <p className={`mt-1 text-2xl font-semibold ${uptime >= 99 ? 'text-emerald-400' : 'text-amber-400'}`}>
            {uptime.toFixed(3)}%
          </p>
        </div>
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
          <p className="text-xs text-slate-500">Total Checks</p>
          <p className="mt-1 text-2xl font-semibold text-white">{totalChecks}</p>
        </div>
        <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-4">
          <p className="text-xs text-slate-500">Failed Checks</p>
          <p className={`mt-1 text-2xl font-semibold ${failedChecks === 0 ? 'text-emerald-400' : 'text-rose-400'}`}>
            {failedChecks}
          </p>
        </div>
      </div>

      {/* Failure Timeline */}
      <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 overflow-hidden">
        <div className="px-5 py-4 border-b border-white/[0.06]">
          <h3 className="text-sm font-medium text-white">Incident History</h3>
          <p className="text-xs text-slate-500 mt-0.5">Recent failures and errors</p>
        </div>

        {recentFailures.length > 0 ? (
          <div className="divide-y divide-white/[0.04]">
            {recentFailures.map((failure) => (
              <div key={failure.id} className="px-5 py-4 flex items-start gap-4">
                {/* Timeline dot */}
                <div className="relative mt-1">
                  <div className="h-2.5 w-2.5 rounded-full bg-rose-500" />
                  <div className="absolute top-3 left-1/2 -translate-x-1/2 w-px h-full bg-white/[0.06]" />
                </div>

                {/* Content */}
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-sm font-mono text-slate-400">{formatDate(failure.created_at)}</span>
                    <span className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs ${
                      failure.status === 'failure' 
                        ? 'bg-rose-500/10 text-rose-400 border border-rose-500/20'
                        : 'bg-amber-500/10 text-amber-400 border border-amber-500/20'
                    }`}>
                      {failure.status === 'failure' ? 'Failed' : 'Error'}
                    </span>
                    {failure.http_status && (
                      <span className="text-xs text-slate-500">HTTP {failure.http_status}</span>
                    )}
                  </div>
                  {failure.error_message && (
                    <p className="mt-1 text-sm text-slate-500 truncate">{failure.error_message}</p>
                  )}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="px-5 py-12 text-center">
            <div className="inline-flex items-center justify-center w-12 h-12 rounded-full bg-emerald-500/10 mb-3">
              <svg className="w-6 h-6 text-emerald-400" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
              </svg>
            </div>
            <p className="text-sm font-medium text-white">No failures recorded</p>
            <p className="text-xs text-slate-500 mt-1">This monitor is performing well!</p>
          </div>
        )}
      </div>
    </div>
  );
}
