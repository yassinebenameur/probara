'use client';

import { useRouter, useParams } from 'next/navigation';
import { useState } from 'react';
import { MonitorAnalyticsRange, SIPMonitorConfig } from '@/lib/types';
import MonitorForm from '@/components/monitors/MonitorForm';
import MonitorDetailOverview from '@/components/monitors/detail/MonitorDetailOverview';
import { AlertRoutingNotice } from '@/components/monitors/AlertRoutingBadge';
import MonitorDetailHistory from '@/components/monitors/detail/MonitorDetailHistory';
import MonitorDetailJson from '@/components/monitors/detail/MonitorDetailJson';
import { MonitorLocationStrip } from '@/components/monitors/MonitorLocationStrip';
import Page from '@/components/ui/Page';
import Tabs, { TabPanel, useUrlTab } from '@/components/ui/Tabs';
import RangePicker from '@/components/ui/RangePicker';
import Callout from '@/components/ui/Callout';
import Button from '@/components/ui/Button';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useCanWrite, useIsTenantAdmin } from '@/components/providers/CurrentUserProvider';
import { getEffectiveMonitorStatus, monitorTarget } from '@/lib/monitor-utils';
import { formatDateTime, formatDateTimeFull, pluralize, RANGES } from '@/lib/format';
import { isSipCall } from '@/lib/sip-config';
import { RANGE_MS as AGENT_RANGE_MS, type TimeRange as AgentTimeRange } from '@/components/monitors/metric-chart';
import { useMonitorDetail, MONITOR_DETAIL_TABS, HISTORY_WINDOW_MS, OVERVIEW_RESULTS_LIMIT } from '@/components/monitors/detail/useMonitorDetail';
import { useLatestFailureScreenshot } from '@/components/monitors/detail/useLatestFailureScreenshot';
import ConfirmHistoryResetModal from '@/components/monitors/detail/ConfirmHistoryResetModal';
import MonitorDetailAside from '@/components/monitors/detail/MonitorDetailAside';
import MonitorDetailHeader from '@/components/monitors/detail/MonitorDetailHeader';

type OverviewTimeRange = MonitorAnalyticsRange;

const OVERVIEW_RANGES: readonly OverviewTimeRange[] = ['1h', '6h', '24h', '7d', '30d', '90d', '365d'];
const AGENT_RANGES: readonly AgentTimeRange[] = ['1h', '6h', '24h', '7d'];

// Monitors that never execute a check of their own: a group rolls up its
// members, agent and push monitors are fed by heartbeats (the API refuses
// run-now for all three; shared/models PassiveMonitorTypes is the source).
// Run check and the timeout fact are hidden for them.
const PASSIVE_TYPES = new Set(['group', 'agent', 'push']);

export default function EditMonitorPage() {
  const router = useRouter();
  const params = useParams();
  const id = params.id as string;
  const canWrite = useCanWrite();
  const isTenantAdmin = useIsTenantAdmin();

  const [activeTab, setActiveTab] = useUrlTab(MONITOR_DETAIL_TABS);
  const [agentTimeRange, setAgentTimeRange] = useState<AgentTimeRange>('24h');
  const [overviewRange, setOverviewRange] = useState<OverviewTimeRange>('24h');
  const {
    monitor,
    loading,
    saving,
    error,
    results,
    historyResults,
    historyAnalytics,
    analytics,
    resultsLoading,
    analyticsLoading,
    slaTarget,
    targetSla,
    tenantRetentionDays,
    runningCheck,
    confirmRunCall,
    setConfirmRunCall,
    togglingEnabled,
    confirmDelete,
    setConfirmDelete,
    deleting,
    clearingHistory,
    isHistoryResetModalOpen,
    historyResetConfirmation,
    setHistoryResetConfirmation,
    handleSubmit,
    handleDelete,
    handleRunCheck,
    handleToggleEnabled,
    openHistoryResetModal,
    closeHistoryResetModal,
    handleClearHistory,
  } = useMonitorDetail(id, activeTab, overviewRange);

  const status = monitor ? getEffectiveMonitorStatus(monitor, results?.results || []) : 'unknown';
  const { latestSyntheticBrowserResult, screenshotBlobURL, screenshotLoading } = useLatestFailureScreenshot(
    monitor,
    results
  );

  if (loading) {
    // The header's shape, so the page does not jump when the monitor lands;
    // the overview brings its own skeleton.
    return (
      <Page width="detail">
        <div className="mb-7 space-y-3" aria-busy="true" aria-label="Loading monitor">
          <div className="h-3 w-40 animate-pulse rounded bg-well-hover" />
          <div className="h-7 w-72 animate-pulse rounded bg-well-hover" />
          <div className="h-4 w-96 max-w-full animate-pulse rounded bg-well" />
        </div>
        <div className="h-9 w-80 max-w-full animate-pulse rounded-xl bg-well" />
        <div className="glass h-36 animate-pulse" />
      </Page>
    );
  }

  if (error || !monitor) {
    return (
      <div className="flex h-64 flex-col items-center justify-center gap-3 text-center">
        <p className="text-sm text-rose-300">{error || 'Monitor not found'}</p>
        <Button variant="ghost" size="sm" onClick={() => router.push('/monitors')}>
          Back to monitors
        </Button>
      </div>
    );
  }

  const tabs = [
    { id: 'overview', label: 'Overview' },
    { id: 'history', label: 'History' },
    { id: 'settings', label: 'Settings' },
    { id: 'json', label: 'JSON' },
  ] as const;
  const selectedWindowMs =
    activeTab === 'history'
      ? HISTORY_WINDOW_MS
      : monitor.type === 'agent'
        ? AGENT_RANGE_MS[agentTimeRange]
        : (RANGES[overviewRange].ms ?? 0);
  const boundedRetentionDays =
    tenantRetentionDays && tenantRetentionDays > 0 ? tenantRetentionDays : null;
  const retentionWindowMs =
    boundedRetentionDays !== null
      ? boundedRetentionDays * 24 * 60 * 60 * 1000
      : null;
  const showRetentionWarning =
    (activeTab === 'overview' || activeTab === 'history') &&
    retentionWindowMs !== null &&
    selectedWindowMs > retentionWindowMs;

  const isPassive = PASSIVE_TYPES.has(monitor.type);
  // A SIP test call places real calls: running, pausing or resuming it is a
  // tenant admin's (the API refuses anyone else), and a run asks first.
  const isCall = isSipCall(monitor.type, monitor.config);
  const canOperate = isCall ? isTenantAdmin : canWrite;
  const runCallCount = Math.max(1, monitor.location_ids?.length ?? 0);
  const runCallSendsAudio = isCall && Boolean((monitor.config as SIPMonitorConfig | undefined)?.call?.media);
  const target = monitorTarget(monitor);
  const lastCheckAt = results?.results?.[0]?.created_at ?? analytics?.summary?.latest_check_at;
  const isGroup = monitor.type === 'group';

  // Side column, most specific first: what this monitor is made of or
  // talking to (members, the server, the query), then its standing
  // configuration, then its place in the dependency graph.
  const aside = (
    <MonitorDetailAside
      monitor={monitor}
      results={results}
      isPassive={isPassive}
      lastCheckAt={lastCheckAt}
      latestSyntheticBrowserResult={latestSyntheticBrowserResult}
      screenshotBlobURL={screenshotBlobURL}
      screenshotLoading={screenshotLoading}
    />
  );

  return (
    <Page width="detail">
      <MonitorDetailHeader
        monitor={monitor}
        status={status}
        target={target}
        isPassive={isPassive}
        isCall={isCall}
        isGroup={isGroup}
        lastCheckAt={lastCheckAt}
        canWrite={canWrite}
        canOperate={canOperate}
        runningCheck={runningCheck}
        togglingEnabled={togglingEnabled}
        onRunCheck={isCall ? () => setConfirmRunCall(true) : handleRunCheck}
        onToggleEnabled={handleToggleEnabled}
        onEditSettings={() => setActiveTab('settings')}
        onClearHistory={openHistoryResetModal}
        onDelete={() => setConfirmDelete(true)}
      />

      <AlertRoutingNotice routing={monitor.alert_routing} enabled={monitor.enabled} />

      {!monitor.enabled && (
        <Callout tone="info">
          Paused. No checks run and no alerts are sent until it is resumed.
          {lastCheckAt && (
            <>
              {' '}Last check <span title={formatDateTimeFull(lastCheckAt)}>{formatDateTime(lastCheckAt)}</span>.
            </>
          )}
        </Callout>
      )}

      {monitor.in_maintenance && (
        <Callout tone="info">
          Under maintenance
          {monitor.maintenance_until ? (
            <>
              {' '}until <span title={formatDateTimeFull(monitor.maintenance_until)}>{formatDateTime(monitor.maintenance_until)}</span>
            </>
          ) : null}
          . Alerts are suppressed; checks keep running.
        </Callout>
      )}

      <Tabs
        tabs={tabs}
        active={activeTab}
        onChange={setActiveTab}
        label="Monitor sections"
        idBase="monitor"
        trailing={
          activeTab === 'overview' ? (
            monitor.type === 'agent' ? (
              <RangePicker ranges={AGENT_RANGES} value={agentTimeRange} onChange={setAgentTimeRange} />
            ) : (
              <RangePicker ranges={OVERVIEW_RANGES} value={overviewRange} onChange={setOverviewRange} />
            )
          ) : activeTab === 'history' ? (
            <span className="text-xs text-slate-500">{RANGES['7d'].long}</span>
          ) : undefined
        }
      />

      {monitor.locations && monitor.locations.length > 0 && (activeTab === 'overview' || activeTab === 'history') && (
        <MonitorLocationStrip
          locations={monitor.locations}
          quorum={monitor.location_quorum ?? 1}
        />
      )}

      {showRetentionWarning && (
        <Callout tone="warning">
          Data retention is set to {boundedRetentionDays} day{boundedRetentionDays === 1 ? '' : 's'}.
          Older history is deleted, so this view may be partial.
        </Callout>
      )}

      <TabPanel idBase="monitor" id={activeTab}>
      {activeTab === 'overview' && (
        <MonitorDetailOverview
          monitor={monitor}
          results={results?.results || []}
          analytics={analytics}
          loading={resultsLoading || analyticsLoading}
          agentTimeRange={agentTimeRange}
          timeRange={overviewRange}
          slaTarget={slaTarget}
          targetSla={targetSla}
          resultsLimit={OVERVIEW_RESULTS_LIMIT}
          aside={aside}
        />
      )}
      {activeTab === 'history' && (
        <MonitorDetailHistory
          results={historyResults?.results || []}
          analytics={historyAnalytics}
          slaTarget={slaTarget}
          timeoutSeconds={monitor.timeout_seconds}
          intervalSeconds={monitor.interval_seconds}
          createdAt={monitor.created_at}
          loading={resultsLoading}
        />
      )}
      {activeTab === 'settings' && (
        <MonitorForm
          monitor={monitor}
          onSubmit={handleSubmit}
          onCancel={() => router.push('/monitors')}
          loading={saving}
        />
      )}
      {activeTab === 'json' && (
        <MonitorDetailJson monitor={monitor} analytics={analytics} />
      )}
      </TabPanel>

      <ConfirmDialog
        open={confirmRunCall}
        tone="default"
        title="Place real test calls?"
        description={
          <>
            Running this check now places {pluralize(runCallCount, 'real call')}
            {target ? (
              <>
                {' '}to <span className="font-mono text-slate-200">{target}</span>
              </>
            ) : null}
            {runCallCount > 1 ? ', one from each of the monitor’s locations' : ''}
            {runCallSendsAudio ? ', each sending test audio during its hold' : ''}. The carrier may bill them.
          </>
        }
        confirmLabel={runCallCount > 1 ? `Place ${runCallCount} calls` : 'Place call'}
        onConfirm={() => void handleRunCheck()}
        onCancel={() => setConfirmRunCall(false)}
      />

      <ConfirmDialog
        open={confirmDelete}
        title={monitor?.type === 'group' ? 'Delete group' : 'Delete monitor'}
        description={
          monitor
            ? `“${monitor.name}” will be removed along with all of its check history. This cannot be undone.`
            : 'This monitor will be removed along with all of its check history. This cannot be undone.'
        }
        confirmLabel="Delete"
        loading={deleting}
        onConfirm={handleDelete}
        onCancel={() => !deleting && setConfirmDelete(false)}
      />

      {monitor && isHistoryResetModalOpen && (
        <ConfirmHistoryResetModal
          monitorName={monitor.name}
          isGroup={monitor.type === 'group'}
          confirmationText={historyResetConfirmation}
          onConfirmationTextChange={setHistoryResetConfirmation}
          onClose={closeHistoryResetModal}
          onConfirm={handleClearHistory}
          loading={clearingHistory}
        />
      )}
    </Page>
  );
}
