'use client';

import { CheckResult, Monitor, MonitorAnalyticsResponse, MonitorAnalyticsRange, AgentMonitorConfig } from '@/lib/types';
import AgentMetricsView from './AgentMetricsView';
import type { TimeRange as AgentTimeRange } from './AgentMetricsView';
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
  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="text-slate-500">Loading monitor data...</div>
      </div>
    );
  }

  if (monitor.type === 'agent') {
    return (
      <AgentMetricsView
        results={results}
        loading={loading}
        timeRange={agentTimeRange}
        onTimeRangeChange={onAgentTimeRangeChange}
        thresholds={(monitor.config as AgentMonitorConfig | undefined)?.metric_thresholds}
      />
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
