'use client';

import { useCallback, useEffect, useState } from 'react';
import { History, KeyRound, RotateCcw } from 'lucide-react';

import type { Monitor } from '@/lib/types';
import { diffMonitorVersion, getAlertChannels, getLocations, getMonitorVersions, restoreMonitorVersion } from '@/lib/api';
import { getAllMonitors } from '@/lib/monitor-list';
import {
  formatVersionValue,
  summarizeChangedFields,
  VERSION_SOURCE_LABEL,
  versionActorLabel,
  versionFieldLabel,
  type MonitorVersionDiff,
  type MonitorVersionListResponse,
  type MonitorVersionSummary,
} from '@/lib/monitor-versions';
import { formatDateTime, formatDateTimeFull, pluralize } from '@/lib/format';
import Panel from '@/components/ui/Panel';
import Pill from '@/components/ui/Pill';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import EmptyState from '@/components/ui/EmptyState';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { RelativeTime } from '@/components/ui/RelativeTime';
import { FOCUS_RING_INSET } from '@/components/ui/focus';
import { SELECTED_SEGMENT } from '@/components/ui/roving';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';

type CompareMode = 'previous' | 'current';

const COMPARE_OPTIONS = [
  { value: 'previous', label: 'Previous', title: 'What this version changed' },
  { value: 'current', label: 'Current', title: 'What restoring this version would change now' },
] as const;

interface MonitorConfigHistoryProps {
  monitor: Monitor;
  /** Called with the monitor a restore wrote, so the page shows it. */
  onRestored: (monitor: Monitor) => void;
}

/**
 * The monitor's Config history tab: every recorded version of its
 * configuration (masked, so secrets are never shown or restored), what each
 * changed, a diff against the previous version or the live monitor, and
 * restore. Versions come from the API's monitors service
 * (services/monitors/versions.go).
 */
export default function MonitorConfigHistory({ monitor, onRestored }: MonitorConfigHistoryProps) {
  const canWrite = useCanWrite();
  const { showToast } = useToast();
  const [list, setList] = useState<MonitorVersionListResponse | null>(null);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<number | null>(null);
  const [compare, setCompare] = useState<CompareMode>('previous');
  const [diff, setDiff] = useState<MonitorVersionDiff | null>(null);
  const [diffError, setDiffError] = useState('');
  const [names, setNames] = useState<Record<string, string>>({});
  const [confirmRestore, setConfirmRestore] = useState(false);
  const [restoreDiff, setRestoreDiff] = useState<MonitorVersionDiff | null>(null);
  const [restoring, setRestoring] = useState(false);

  const loadVersions = useCallback(async () => {
    try {
      const response = await getMonitorVersions(monitor.id);
      setList(response);
      setError('');
      setSelected((current) =>
        current !== null && response.items.some((item) => item.version === current)
          ? current
          : response.items[0]?.version ?? null,
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load config history');
    }
  }, [monitor.id]);

  useEffect(() => {
    void loadVersions();
  }, [loadVersions, monitor.updated_at]);

  // Names for the IDs a snapshot carries (channels, locations, monitors).
  // Best effort: an unknown or deleted one shows its ID.
  useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([
      getAllMonitors(),
      getAlertChannels({ page_size: 100 }),
      getLocations({ page_size: 100 }),
    ]).then(([monitors, channels, locations]) => {
      if (cancelled) return;
      const next: Record<string, string> = {};
      if (monitors.status === 'fulfilled') monitors.value.forEach((m) => (next[m.id] = m.name));
      if (channels.status === 'fulfilled') (channels.value.items || []).forEach((c) => (next[c.id] = c.name));
      if (locations.status === 'fulfilled') (locations.value.items || []).forEach((l) => (next[l.id] = l.name));
      setNames(next);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (selected === null) {
      setDiff(null);
      return;
    }
    let cancelled = false;
    setDiffError('');
    diffMonitorVersion(monitor.id, selected, compare === 'current' ? 'current' : undefined)
      .then((response) => !cancelled && setDiff(response))
      .catch((err) => !cancelled && setDiffError(err instanceof Error ? err.message : 'Failed to load the diff'));
    return () => {
      cancelled = true;
    };
  }, [monitor.id, selected, compare, list]);

  const items = list?.items ?? [];
  const latest = items[0]?.version ?? null;
  const current = items.find((item) => item.version === selected) ?? null;

  const openRestore = async () => {
    if (selected === null) return;
    try {
      setRestoreDiff(await diffMonitorVersion(monitor.id, selected, 'current'));
    } catch {
      setRestoreDiff(null);
    }
    setConfirmRestore(true);
  };

  const handleRestore = async () => {
    if (selected === null) return;
    setRestoring(true);
    try {
      const response = await restoreMonitorVersion(monitor.id, selected);
      onRestored(response.monitor);
      setConfirmRestore(false);
      const dropped = response.dropped_references.length;
      showToast(
        dropped > 0
          ? `Restored version ${selected}. ${pluralize(dropped, 'reference')} no longer exist and ${dropped === 1 ? 'was' : 'were'} left out.`
          : `Restored version ${selected}.`,
        dropped > 0 ? 'warning' : 'success',
      );
      await loadVersions();
    } catch (err) {
      showToast(err instanceof Error ? err.message : 'Failed to restore the version', 'error');
    } finally {
      setRestoring(false);
    }
  };

  if (error) {
    return <Callout tone="danger">{error}</Callout>;
  }
  if (!list) {
    return <div className="glass h-48 animate-pulse" aria-busy="true" aria-label="Loading config history" />;
  }
  if (items.length === 0) {
    return (
      <EmptyState
        icon={<History className="h-5 w-5" aria-hidden="true" />}
        title="No config history yet"
        description="Every change to this monitor's configuration from now on is recorded here. History is not backfilled."
      />
    );
  }

  const restoreChanges = restoreDiff?.changes.length ?? 0;

  return (
    <div className="space-y-4">
      <Callout tone="info">
        History starts at{' '}
        <span title={formatDateTimeFull(list.history_starts_at)}>{formatDateTime(list.history_starts_at)}</span>
        {list.truncated ? `; older versions were pruned (the last ${list.retention_limit} are kept)` : ''}. Secret values
        are never recorded or restored.
      </Callout>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
        <Panel title="Versions" subtitle={pluralize(items.length, 'version')}>
          <ul className="-mx-2 max-h-[32rem] space-y-1 overflow-y-auto" aria-label="Config versions">
            {items.map((item) => (
              <li key={item.version}>
                <VersionRow
                  item={item}
                  latest={item.version === latest}
                  selected={item.version === selected}
                  onSelect={() => setSelected(item.version)}
                />
              </li>
            ))}
          </ul>
        </Panel>

        {current && (
          <Panel
            title={`Version ${current.version}`}
            subtitle={`${VERSION_SOURCE_LABEL[current.source] ?? current.source} · ${versionActorLabel(current)} · ${formatDateTimeFull(current.created_at)}`}
            actions={
              canWrite && current.version !== latest ? (
                <Button size="xs" variant="ghost" icon={<RotateCcw aria-hidden="true" />} onClick={() => void openRestore()}>
                  Restore
                </Button>
              ) : undefined
            }
          >
            <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
              <SegmentedControl
                options={COMPARE_OPTIONS}
                value={compare}
                onChange={setCompare}
                label="Compare with"
                size="xs"
              />
              {current.changed_secret_fields.length > 0 && (
                <span className="inline-flex items-center gap-1.5 text-xs text-slate-400">
                  <KeyRound className="h-3.5 w-3.5" aria-hidden="true" />
                  Secrets changed: {current.changed_secret_fields.map(versionFieldLabel).join(', ')}
                </span>
              )}
            </div>
            {diffError ? (
              <Callout tone="danger">{diffError}</Callout>
            ) : (
              <DiffTable diff={diff} compare={compare} names={names} />
            )}
          </Panel>
        )}
      </div>

      <ConfirmDialog
        open={confirmRestore}
        tone="default"
        title={`Restore version ${selected ?? ''}?`}
        description={
          <>
            {restoreDiff
              ? restoreChanges === 0
                ? 'The monitor already matches this version.'
                : `This changes ${pluralize(restoreChanges, 'field')} of the live monitor (compare with Current to review them).`
              : 'This re-applies the version to the live monitor.'}{' '}
            Secrets keep their current values, pause state is left as it is, and channels, locations, monitors or an
            escalation policy that no longer exist are left out. The restore is recorded as a new version.
          </>
        }
        confirmLabel="Restore"
        loading={restoring}
        onConfirm={() => void handleRestore()}
        onCancel={() => !restoring && setConfirmRestore(false)}
      />
    </div>
  );
}

function VersionRow({
  item,
  latest,
  selected,
  onSelect,
}: {
  item: MonitorVersionSummary;
  latest: boolean;
  selected: boolean;
  onSelect: () => void;
}) {
  const summary =
    item.source === 'restore' && item.restored_from
      ? `Restored version ${item.restored_from}`
      : item.changed_fields.length > 0
        ? summarizeChangedFields(item.changed_fields)
        : item.changed_secret_fields.length > 0
          ? 'Secrets only'
          : 'First recorded version';
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={selected ? 'true' : undefined}
      className={`w-full rounded-lg px-3 py-2.5 text-left transition-colors ${FOCUS_RING_INSET} ${
        selected ? SELECTED_SEGMENT : 'text-slate-300 hover:bg-well-hover hover:text-slate-100'
      }`}
    >
      <span className="flex items-center justify-between gap-2">
        <span className="flex items-center gap-2">
          <span className="font-mono text-ui font-semibold">v{item.version}</span>
          <Pill size="xs" tone="neutral">
            {VERSION_SOURCE_LABEL[item.source] ?? item.source}
          </Pill>
          {latest && (
            <Pill size="xs" tone="info">
              Current
            </Pill>
          )}
        </span>
        <RelativeTime value={item.created_at} className="text-2xs text-slate-500" />
      </span>
      <span className="mt-1 block truncate text-xs text-slate-400">
        {versionActorLabel(item)} · {summary}
        {item.changed_secret_fields.length > 0 && item.changed_fields.length > 0 ? ' · secrets' : ''}
      </span>
    </button>
  );
}

function DiffTable({
  diff,
  compare,
  names,
}: {
  diff: MonitorVersionDiff | null;
  compare: CompareMode;
  names: Record<string, string>;
}) {
  if (!diff) {
    return <div className="h-24 animate-pulse rounded-lg bg-well" aria-busy="true" />;
  }
  if (diff.changes.length === 0) {
    return (
      <p className="text-xs text-slate-500">
        {compare === 'current'
          ? 'The live monitor matches this version.'
          : 'No field changed: only secret values did.'}
      </p>
    );
  }
  // "previous" reads previous → this version; "current" reads the live
  // monitor → this version, i.e. what a restore would write.
  const beforeLabel = compare === 'current' ? 'Live now' : diff.against ? `v${diff.against}` : 'Before';
  const afterLabel = compare === 'current' ? 'After restore' : `v${diff.version}`;
  return (
    <div className="glass-well overflow-x-auto">
      <table className="w-full text-left text-xs">
        <thead className="text-2xs uppercase tracking-wide text-slate-500">
          <tr>
            <th scope="col" className="px-3 py-2 font-medium">Field</th>
            <th scope="col" className="px-3 py-2 font-medium">{beforeLabel}</th>
            <th scope="col" className="px-3 py-2 font-medium">{afterLabel}</th>
          </tr>
        </thead>
        <tbody>
          {diff.changes.map((change) => (
            <tr key={change.path} className="border-t border-divider align-top">
              <th scope="row" className="px-3 py-2 font-medium text-slate-300">
                {versionFieldLabel(change.path)}
              </th>
              <td className="break-all px-3 py-2 font-mono text-rose-300">
                {change.before === undefined ? <span className="text-slate-600">—</span> : formatVersionValue(change.before, names)}
              </td>
              <td className="break-all px-3 py-2 font-mono text-emerald-300">
                {change.after === undefined ? <span className="text-slate-600">—</span> : formatVersionValue(change.after, names)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
