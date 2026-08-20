'use client';

import { CheckResult, Monitor, MonitorAnalyticsResponse, MonitorAnalyticsRange, AgentMonitorConfig } from '@/lib/types';
import AgentMetricsView from './AgentMetricsView';
import MetricExplorer from './MetricExplorer';
import type { TimeRange as AgentTimeRange } from './metric-chart';
import MonitorAnalyticsOverview from './MonitorAnalyticsOverview';

interface MonitorDetailOverviewProps {
  monitor: Monitor;
  results: CheckResult[];
  analytics: MonitorAnalyticsResponse | null;
  loading?: boolean;
  agentTimeRange?: AgentTimeRange;
  onAgentTimeRangeChange?: (range: AgentTimeRange) => void;
  timeRange?: MonitorAnalyticsRange;
  onTimeRangeChange?: (range: MonitorAnalyticsRange) => void;
}

export default function MonitorDetailOverview({
  monitor,
  results,
  analytics,
  loading = false,
  agentTimeRange = '24h',
  onAgentTimeRangeChange,
  timeRange = '24h',
  onTimeRangeChange,
}: MonitorDetailOverviewProps) {
  if (monitor.type === 'agent') {
    // Agent metrics come from the metric store query API (fetched inside the
    // views), not from check results — no results-loading gate here.
    const agentConfig = monitor.config as AgentMonitorConfig | undefined;
    return (
      <div className="space-y-6">
        <AgentMetricsView
          monitorId={monitor.id}
          expectedIntervalSeconds={agentConfig?.expected_interval_seconds ?? monitor.interval_seconds}
          metricRules={agentConfig?.metric_rules}
          timeRange={agentTimeRange}
          onTimeRangeChange={onAgentTimeRangeChange}
        />
        <MetricExplorer monitorId={monitor.id} timeRange={agentTimeRange} />
      </div>
    );
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="text-slate-500">Loading monitor data...</div>
      </div>
    );
  }

  return (
    <MonitorAnalyticsOverview
      monitor={monitor}
      analytics={analytics}
      results={results}
      loading={loading}
      timeRange={timeRange}
      onTimeRangeChange={onTimeRangeChange || (() => undefined)}
    />
  );
}
