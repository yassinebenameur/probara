'use client';

import { useState, useEffect, useMemo, useCallback, useRef } from 'react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import LiveStatus from '@/components/ui/LiveStatus';
import Page from '@/components/ui/Page';
import PageHeader from '@/components/ui/PageHeader';
import Pill from '@/components/ui/Pill';
import RangePicker from '@/components/ui/RangePicker';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import Link from 'next/link';
import { Plus, X } from 'lucide-react';
import { buildActivityTimeline, buildOperationalSummary, countDegraded, downMonitorsHref, needsAttentionTotals, problemMonitorStatus, sortNeedsAttention } from '@/lib/dashboard-view-model';
import { RANGES, rangeStart } from '@/lib/format';
import { useRowFlash } from '@/lib/useRowFlash';
import { useDocumentTitle } from '@/lib/useDocumentTitle';
import ServiceGroupsPanel from '@/components/dashboard/ServiceGroupsPanel';
import { NotificationNudge } from '@/components/layout/NotificationNudge';
import { type TrendPoint, DASHBOARD_RANGE_OPTIONS, type DashboardPageRange, DASHBOARD_LIST_LIMIT, TIMELINE_ROWS, GRID, CELL_ATTENTION, CELL_CHART, CELL_CHANGES, CELL_GROUPS } from '@/components/dashboard/layout';
import { TagFilterPicker } from '@/components/dashboard/TagFilterPicker';
import { NeedsAttentionPanel } from '@/components/dashboard/NeedsAttentionPanel';
import { UptimeResponseChart } from '@/components/dashboard/UptimeResponseChart';
import { LoadingSkeleton } from '@/components/dashboard/LoadingSkeleton';
import { OperationalSummaryCard } from '@/components/dashboard/OperationalSummaryCard';
import { WhatChangedTimeline } from '@/components/dashboard/WhatChangedTimeline';
import { useDashboardData } from '@/components/dashboard/useDashboardData';
import { useProblemTraces } from '@/components/dashboard/useProblemTraces';

export default function DashboardPage() {
  useDocumentTitle('Dashboard');
  const canWrite = useCanWrite();
  const [timeRange, setTimeRange] = useState<DashboardPageRange>('1h');
  const [selectedTags, setSelectedTags] = useState<string[]>([]);
  const {
    dashboard,
    problemMonitorsData,
    recentFailuresData,
    recentAlertsData,
    tenantRetentionDays,
    error,
    sectionsError,
    live,
    retry,
  } = useDashboardData(timeRange, selectedTags);

  const problemMonitors = useMemo(() => problemMonitorsData || [], [problemMonitorsData]);
  const sortedProblems = useMemo(() => sortNeedsAttention(problemMonitors), [problemMonitors]);
  const problemFlash = useRowFlash(sortedProblems, (m) => m.monitor_id, (m) => problemMonitorStatus(m));

  const { traces, observe } = useProblemTraces(sortedProblems, problemMonitorsData);

  const availableTags = useMemo(() => dashboard?.available_tags || [], [dashboard]);

  useEffect(() => {
    if (availableTags.length === 0) {
      setSelectedTags((current) => (current.length === 0 ? current : []));
      return;
    }
    setSelectedTags((current) => {
      const next = current.filter((tag) => availableTags.includes(tag));
      return next.length === current.length ? current : next;
    });
  }, [availableTags]);

  const toggleTag = useCallback((tag: string) => {
    setSelectedTags((current) =>
      current.includes(tag)
        ? current.filter((value) => value !== tag)
        : [...current, tag].sort((a, b) => a.localeCompare(b))
    );
  }, []);

  const clearTags = useCallback(() => {
    setSelectedTags((current) => (current.length === 0 ? current : []));
  }, []);

  const trendData = useMemo<TrendPoint[]>(() => {
    return (dashboard?.trend || []).map((point) => ({
      date: point.label,
      uptime: point.total_checks > 0 ? point.uptime : null,
      responseTime: point.total_checks > 0 ? point.response_time : null,
      total: point.total_checks,
    }));
  }, [dashboard]);

  const populatedTrendPoints = useMemo(() => trendData.filter((d) => d.total > 0).length, [trendData]);
  const hasEnoughTrendData = populatedTrendPoints >= 2;

  const opsSummary = dashboard?.ops_summary;
  const degradedMonitors = useMemo(() => countDegraded(problemMonitorsData), [problemMonitorsData]);
  const recentFailures = useMemo(() => recentFailuresData || [], [recentFailuresData]);
  const recentAlerts = useMemo(() => recentAlertsData || [], [recentAlertsData]);

  const rangeLabel = RANGES[timeRange].long;
  const stats = dashboard?.stats;
  const totalMonitors = stats?.total_monitors || 0;
  const hasTagFilter = selectedTags.length > 0;
  const noMatchingMonitors = hasTagFilter && totalMonitors === 0;
  const operationalSummary = useMemo(
    () => buildOperationalSummary({
      opsSummary,
      problemMonitorsCount: problemMonitors.length,
      degradedMonitors,
      recentFailuresCount: recentFailures.length,
      overallUptime: stats?.overall_uptime ?? null,
      avgResponseMs: stats?.avg_response_ms || 0,
      totalMonitors,
      httpMonitors: stats?.http_monitors,
      rangeLabel,
      tags: selectedTags,
    }),
    [opsSummary, problemMonitors.length, degradedMonitors, recentFailures.length, stats?.avg_response_ms, stats?.overall_uptime, stats?.http_monitors, totalMonitors, rangeLabel, selectedTags],
  );
  const attentionTotals = useMemo(
    () => needsAttentionTotals(problemMonitorsData, DASHBOARD_LIST_LIMIT, {
      total: operationalSummary.attentionCount,
      unhealthy: (opsSummary?.down_monitors ?? 0) + (degradedMonitors ?? 0),
    }),
    [problemMonitorsData, operationalSummary.attentionCount, opsSummary?.down_monitors, degradedMonitors],
  );
  const downHref = useMemo(() => downMonitorsHref(selectedTags), [selectedTags]);
  const timelineItems = useMemo(
    () => buildActivityTimeline({
      failures: recentFailures,
      alerts: recentAlerts,
      monitors: problemMonitors,
      since: rangeStart(timeRange),
    }).slice(0, TIMELINE_ROWS),
    [problemMonitors, recentAlerts, recentFailures, timeRange],
  );
  // A row that appears or changes event between refreshes flashes once.
  const timelineFlash = useRowFlash(timelineItems, (item) => item.id, (item) => `${item.event}|${item.occurredAt}`, {
    includeNew: true,
  });

  // The headline, spoken when a refresh changes it (not on first load).
  const [announcement, setAnnouncement] = useState('');
  const lastHeadline = useRef<string | null>(null);
  useEffect(() => {
    if (!dashboard) return;
    const headline = operationalSummary.label;
    if (lastHeadline.current !== null && lastHeadline.current !== headline) setAnnouncement(`Dashboard updated: ${headline}`);
    lastHeadline.current = headline;
  }, [dashboard, operationalSummary.label]);

  const selectedRangeDays = Math.floor((RANGES[timeRange].ms ?? 0) / 86_400_000);
  const showRetentionWarning =
    tenantRetentionDays !== null && tenantRetentionDays > 0 && selectedRangeDays > tenantRetentionDays;

  if (dashboard === null && !error) return <LoadingSkeleton />;

  return (
    <Page width="data">
      <p role="status" className="sr-only">
        {announcement}
      </p>
      <NotificationNudge unroutedMonitors={opsSummary?.unrouted_monitors ?? 0} />

      {error && (
        <Callout
          tone="danger"
          action={<Button variant="danger" size="xs" onClick={retry}>Retry</Button>}
        >
          {error}
        </Callout>
      )}

      <PageHeader
        title="Dashboard"
        titleAside={
          <LiveStatus
            updatedAt={live.updatedAt}
            refreshing={live.refreshing}
            failed={live.failed}
            onRefresh={retry}
            refreshLabel="Refresh dashboard"
          />
        }
        action={
          <>
            {/* The range scopes every panel on the page, so it lives here. */}
            <RangePicker ranges={DASHBOARD_RANGE_OPTIONS} value={timeRange} onChange={setTimeRange} />
            <TagFilterPicker
              availableTags={availableTags}
              selectedTags={selectedTags}
              onToggleTag={toggleTag}
              onClear={clearTags}
            />
            {canWrite && (
              <Button variant="accent" icon={<Plus className="h-4 w-4" strokeWidth={1.75} />} asChild>
                <Link href="/monitors/new">Add monitor</Link>
              </Button>
            )}
          </>
        }
      />

      {hasTagFilter && (
        <div className="flex flex-wrap items-center gap-2 text-ui">
          <span className="text-slate-400">Scoped to</span>
          {selectedTags.map((tag) => (
            <Pill key={tag} tone="neutral">
              {tag}
            </Pill>
          ))}
          <Button variant="subtle" size="xs" icon={<X className="h-3.5 w-3.5" strokeWidth={1.75} />} onClick={clearTags}>
            Clear
          </Button>
        </div>
      )}

      {showRetentionWarning && (
        <Callout tone="warning">
          Data retention is set to {tenantRetentionDays} day{tenantRetentionDays === 1 ? '' : 's'}.
          Older history is deleted, so this {timeRange} view may be partial.
        </Callout>
      )}

      {noMatchingMonitors && (
        <Callout tone="info" title="No monitors match the selected tag combination.">
          Adjust the tag filter to broaden the dashboard scope.
        </Callout>
      )}

      {dashboard && (
        <div className="animate-fade-in">
          <OperationalSummaryCard summary={operationalSummary} />
        </div>
      )}

      {/* Without a summary there is nothing true to draw: the error callout
          above carries Retry, and empty panels would read as an all-clear. */}
      {dashboard && (
        <div className={`animate-fade-in ${GRID}`}>
          <div className={CELL_ATTENTION}>
            <NeedsAttentionPanel
              monitors={sortedProblems}
              traces={traces}
              total={attentionTotals.total}
              unhealthyCount={attentionTotals.unhealthy}
              downHref={downHref}
              loading={problemMonitorsData === null && !sectionsError}
              loadFailed={sectionsError && problemMonitorsData === null}
              onRetry={retry}
              noMatchingMonitors={noMatchingMonitors}
              rangeLabel={rangeLabel}
              observe={observe}
              flashClass={problemFlash.flashClass}
            />
          </div>
          <div className={CELL_CHART}>
            <UptimeResponseChart
              trendData={trendData}
              hasEnoughTrendData={hasEnoughTrendData}
              noMatchingMonitors={noMatchingMonitors}
              rangeLabel={rangeLabel}
            />
          </div>
          <div className={CELL_CHANGES}>
            <WhatChangedTimeline
              items={timelineItems}
              loading={(recentFailuresData === null || recentAlertsData === null) && !sectionsError}
              loadFailed={sectionsError && (recentFailuresData === null || recentAlertsData === null)}
              onRetry={retry}
              rangeLabel={rangeLabel}
              flashClass={timelineFlash.flashClass}
            />
          </div>
          <div className={CELL_GROUPS}>
            <ServiceGroupsPanel
              groupTags={dashboard.group_tags ?? []}
              groups={dashboard.groups ?? []}
              range={timeRange}
              filterTags={selectedTags}
              monitorStates={problemMonitors}
            />
          </div>
        </div>
      )}
    </Page>
  );
}
