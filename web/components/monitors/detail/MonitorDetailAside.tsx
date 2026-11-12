'use client';

import PrometheusDetails from '@/components/monitors/PrometheusDetails';
import Image from 'next/image';
import { Monitor, MonitorResultsResponse, CheckResult } from '@/lib/types';
import { GroupMembersCard } from '@/components/monitors/GroupMembersCard';
import { MonitorDependenciesCard } from '@/components/monitors/MonitorDependenciesCard';
import MonitorFactsCard, { DetailList } from '@/components/monitors/MonitorFactsCard';
import Panel from '@/components/ui/Panel';
import { formatDateTime, formatDateTimeFull } from '@/lib/format';
import { serverFactRows } from '@/components/monitors/detail/ServerFactRows';

interface MonitorDetailAsideProps {
  monitor: Monitor;
  results: MonitorResultsResponse | null;
  /** The monitor never runs a check of its own (group, agent, push). */
  isPassive: boolean;
  lastCheckAt?: string;
  latestSyntheticBrowserResult?: CheckResult;
  screenshotBlobURL: string | null;
  screenshotLoading: boolean;
}

// Side column, most specific first: what this monitor is made of or
// talking to (members, the server, the query), then its standing
// configuration, then its place in the dependency graph.
export default function MonitorDetailAside({
  monitor,
  results,
  isPassive,
  lastCheckAt,
  latestSyntheticBrowserResult,
  screenshotBlobURL,
  screenshotLoading,
}: MonitorDetailAsideProps) {
  const serverRows = serverFactRows(monitor, results?.results || []);
  const isGroup = monitor.type === 'group';

  return (
    <>
      {isGroup && <GroupMembersCard groupId={monitor.id} />}

      {monitor.type === 'prometheus' && (
        <Panel title="Query">
          <PrometheusDetails monitor={monitor} result={results?.results?.[0]} />
        </Panel>
      )}

      {serverRows.length > 0 && (
        <Panel title="Server">
          <DetailList>{serverRows}</DetailList>
        </Panel>
      )}

      <MonitorFactsCard monitor={monitor} passive={isPassive} lastCheckAt={lastCheckAt} />

      <MonitorDependenciesCard monitorId={monitor.id} monitorType={monitor.type} />

      {monitor.type === 'synthetic_browser' && (
        <Panel title="Latest failure screenshot">
          {screenshotLoading ? (
            <p className="text-ui text-slate-400">Loading screenshot...</p>
          ) : screenshotBlobURL ? (
            <>
              <div className="glass-well overflow-hidden p-2">
                <Image
                  src={screenshotBlobURL}
                  alt="Latest synthetic browser failure screenshot"
                  width={1200}
                  height={675}
                  className="h-auto max-h-[220px] w-full rounded-lg object-contain"
                  unoptimized
                  loading="lazy"
                />
              </div>
              <p className="mt-2 text-xs text-slate-500">
                {latestSyntheticBrowserResult
                  ? <span title={formatDateTimeFull(latestSyntheticBrowserResult.created_at)}>Captured {formatDateTime(latestSyntheticBrowserResult.created_at)}</span>
                  : 'Latest run'}
              </p>
            </>
          ) : (
            <p className="text-ui text-slate-400">
              No screenshot available yet. A failed run with screenshot capture enabled is required.
            </p>
          )}
        </Panel>
      )}
    </>
  );
}
