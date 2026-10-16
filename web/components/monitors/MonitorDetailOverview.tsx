'use client';

import { ReactNode } from 'react';
import { CheckResult, Monitor, MonitorAnalyticsResponse, MonitorAnalyticsRange, AgentMonitorConfig } from '@/lib/types';
import type { Sla } from '@/lib/sla';
import AgentMetricsView from './AgentMetricsView';
import MetricExplorer from './MetricExplorer';
import type { TimeRange as AgentTimeRange } from './metric-chart';
import MonitorAnalyticsOverview from './MonitorAnalyticsOverview';
import MongoClusterPanels from './MongoClusterPanels';
import SipCallPanels from './SipCallPanels';
import { isSipCall } from '@/lib/sip-config';

interface MonitorDetailOverviewProps {
  monitor: Monitor;
  results: CheckResult[];
  analytics: MonitorAnalyticsResponse | null;
  loading?: boolean;
  /** Agent monitors: the range picked in the tabs row. */
  agentTimeRange?: AgentTimeRange;
  timeRange?: MonitorAnalyticsRange;
  slaTarget: number;
  targetSla?: Sla;
  resultsLimit: number;
  /** Side column (dependencies, configuration); stacks under the charts below xl. */
  aside?: ReactNode;
}

// First-load placeholder in the shape of the stat strip and the trace, so
// the page does not jump when analytics arrive.
function OverviewSkeleton() {
  return (
    <div className="glass overflow-hidden" aria-busy="true" aria-label="Loading monitor data">
      <div className="grid grid-cols-2 gap-6 px-5 py-4 sm:grid-cols-4">
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className="space-y-2">
            <div className="h-3 w-16 animate-pulse rounded bg-well-hover" />
            <div className="h-6 w-24 animate-pulse rounded bg-well-hover" />
          </div>
        ))}
      </div>
      <div className="border-t border-divider px-5 pb-4 pt-3.5">
        <div className="h-8 animate-pulse rounded bg-well" />
      </div>
    </div>
  );
}

export default function MonitorDetailOverview({
  monitor,
  results,
  analytics,
  loading = false,
  agentTimeRange = '24h',
  timeRange = '24h',
  slaTarget,
  targetSla,
  resultsLimit,
  aside,
}: MonitorDetailOverviewProps) {
  if (monitor.type === 'agent') {
    // Agent metrics come from the metric store query API (fetched inside the
    // views), not from check results — no results-loading gate here.
    const agentConfig = monitor.config as AgentMonitorConfig | undefined;
    return (
      <div className={aside ? 'grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1fr)_320px]' : undefined}>
        <div className="min-w-0 space-y-6">
          <AgentMetricsView
            monitorId={monitor.id}
            expectedIntervalSeconds={agentConfig?.expected_interval_seconds ?? monitor.interval_seconds}
            metricRules={agentConfig?.metric_rules}
            timeRange={agentTimeRange}
          />
          <MetricExplorer monitorId={monitor.id} timeRange={agentTimeRange} />
        </div>
        {aside && <div className="grid grid-cols-1 content-start gap-4 md:grid-cols-2 xl:grid-cols-1">{aside}</div>}
      </div>
    );
  }

  if (loading && !analytics) {
    return <OverviewSkeleton />;
  }

  return (
    <div className="space-y-6">
      <MonitorAnalyticsOverview
        monitor={monitor}
        analytics={analytics}
        results={results}
        timeRange={timeRange}
        slaTarget={slaTarget}
        targetSla={targetSla}
        resultsLimit={resultsLimit}
        aside={aside}
      />
      {monitor.type === 'mongodb' && (
        <MongoClusterPanels monitor={monitor} timeRange={timeRange} latestResultAt={results[0]?.created_at} />
      )}
      {isSipCall(monitor.type, monitor.config) && (
        <SipCallPanels monitor={monitor} timeRange={timeRange} latestResultAt={results[0]?.created_at} />
      )}
    </div>
  );
}
