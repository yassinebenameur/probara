'use client';

import { useCallback, useEffect, useState } from 'react';
import { History, KeyRound, RotateCcw } from 'lucide-react';

import type { Monitor } from '@/lib/types';
import { diffMonitorVersion, getAlertChannels, getLocations, getMonitorVersions, restoreMonitorVersion } from '@/lib/api';
import { getAllMonitors } from '@/lib/monitor-list';
import {
  droppedReferenceLabel,
  formatVersionValue,
  secretChangeText,
  summarizeVersion,
  VERSION_SOURCE_LABEL,
  versionActorLabel,
  versionFieldLabel,
  type MonitorVersionDiff,
  type MonitorVersionListResponse,
  type MonitorVersionSummary,
} from '@/lib/monitor-versions';
import { formatDateTime, formatDateTimeFull, pluralize } from '@/lib/format';
import { errorMessage } from '@/lib/errors';
import Panel from '@/components/ui/Panel';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import EmptyState from '@/components/ui/EmptyState';
import IconButton from '@/components/ui/IconButton';
import Pagination from '@/components/ui/Pagination';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { RelativeTime } from '@/components/ui/RelativeTime';
import { FOCUS_RING_INSET } from '@/components/ui/focus';
import { SELECTED_SEGMENT } from '@/components/ui/roving';
import { useToast } from '@/components/ui/ToastProvider';
import { useCanWrite, useIsTenantAdmin } from '@/components/providers/CurrentUserProvider';
import { isSipCall } from '@/lib/sip-config';

type CompareMode = 'previous' | 'current';

const COMPARE_OPTIONS = [
  { value: 'previous', label: 'Previous', title: 'What this version changed' },
  { value: 'current', label: 'Current', title: 'What restoring this version would change now' },
] as const;

const PAGE_SIZE = 20;

interface MonitorConfigHistoryProps {
  monitor: Monitor;
  /** Called with the monitor a restore wrote, so the page shows it. */
  onRestored: (monitor: Monitor) => void;
}

/**
 * The monitor's Changes tab: every recorded version of its configuration
 * (masked, so secret values are never shown, stored or restored), what each
 * changed, a diff against the previous version or the live monitor, and
 * restore. Versions are written by the API's monitors service on every save
 * (services/monitors/versions.go).
 */
export default function MonitorConfigHistory({ monitor, onRestored }: MonitorConfigHistoryProps) {
  const canWrite = useCanWrite();
  const isTenantAdmin = useIsTenantAdmin();
  // A SIP test-call monitor is changed by tenant admins only (the API
  // refuses anyone else), restores included.
  const canRestore = isSipCall(monitor.type, monitor.config) ? isTenantAdmin : canWrite;
  const { showToast } = useToast();
  const [page, setPage] = useState(1);
  const [list, setList] = useState<MonitorVersionListResponse | null>(null);
  const [latest, setLatest] = useState<number | null>(null);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<number | null>(null);
  const [compare, setCompare] = useState<CompareMode>('previous');
  const [diff, setDiff] = useState<MonitorVersionDiff | null>(null);
  const [diffError, setDiffError] = useState('');
  const [names, setNames] = useState<Record<string, string>>({});
  const [restoreTarget, setRestoreTarget] = useState<number | null>(null);
  const [restoreDiff, setRestoreDiff] = useState<MonitorVersionDiff | null>(null);
  const [restoring, setRestoring] = useState(false);
  const [reenter, setReenter] = useState<{ version: number; secrets: string[] } | null>(null);

  const loadVersions = useCallback(async () => {
    try {
      const response = await getMonitorVersions(monitor.id, { page, page_size: PAGE_SIZE });
      setList(response);
      setError('');
      if (page === 1) setLatest(response.items[0]?.version ?? null);
      setSelected((current) =>
        current !== null && response.items.some((item) => item.version === current)
          ? current
          : (response.items[0]?.version ?? null),
      );
    } catch (err) {
      setError(errorMessage(err, 'Failed to load the change history'));
    }
  }, [monitor.id, page]);

  // A save from the Settings tab bumps updated_at: reload to show it.
  useEffect(() => {
    void loadVersions();
  }, [loadVersions, monitor.updated_at]);

  // Names for the IDs a snapshot carries (channels, locations, monitors).
  // Best effort: an unknown or deleted one shows its ID.
  useEffect(() => {
    let cancelled = false;
    void Promise.allSettled([getAllMonitors(), getAlertChannels({ page_size: 100 }), getLocations({ page_size: 100 })]).then(
      ([monitors, channels, locations]) => {
        if (cancelled) return;
        const next: Record<string, string> = {};
        if (monitors.status === 'fulfilled') monitors.value.forEach((m) => (next[m.id] = m.name));
        if (channels.status === 'fulfilled') (channels.value.items || []).forEach((c) => (next[c.id] = c.name));
        if (locations.status === 'fulfilled') (locations.value.items || []).forEach((l) => (next[l.id] = l.name));
        setNames(next);
      },
    );
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
    setDiff(null);
    setDiffError('');
    diffMonitorVersion(monitor.id, selected, compare === 'current' ? 'current' : undefined)
      .then((response) => !cancelled && setDiff(response))
      .catch((err) => !cancelled && setDiffError(errorMessage(err, 'Failed to load the diff')));
    return () => {
      cancelled = true;
    };
  }, [monitor.id, selected, compare, list]);

  const items = list?.items ?? [];
  const current = items.find((item) => item.version === selected) ?? null;
  const totalPages = list ? Math.max(1, Math.ceil(list.total / list.page_size)) : 1;

  const openRestore = async (version: number) => {
    setRestoreTarget(version);
    setRestoreDiff(null);
    try {
      setRestoreDiff(await diffMonitorVersion(monitor.id, version, 'current'));
    } catch {
      // The dialog still offers the restore; it just cannot count the changes.
    }
  };

  const handleRestore = async () => {
    if (restoreTarget === null) return;
    const version = restoreTarget;
    setRestoring(true);
    try {
      const response = await restoreMonitorVersion(monitor.id, version);
      onRestored(response.monitor);
      setRestoreTarget(null);
      setReenter(response.secrets_to_reenter.length > 0 ? { version, secrets: response.secrets_to_reenter } : null);
      const dropped = response.dropped_references.map((reference) => droppedReferenceLabel(reference, names));
      const notes: string[] = [];
      if (dropped.length > 0) notes.push(`Left out: ${dropped.join(', ')}.`);
      if (response.secrets_to_reenter.length > 0) {
        notes.push(`${pluralize(response.secrets_to_reenter.length, 'secret')} to enter again.`);
      }
      showToast(
        response.version === undefined
          ? `The monitor already matches version ${version}.`
          : `Restored version ${version} as version ${response.version}.${notes.length > 0 ? ` ${notes.join(' ')}` : ''}`,
        notes.length > 0 ? 'warning' : 'success',
      );
      if (page === 1) await loadVersions();
      else setPage(1);
    } catch (err) {
      showToast(errorMessage(err, 'Failed to restore the version'), 'error');
    } finally {
      setRestoring(false);
    }
  };

  if (error) {
    return <Callout tone="danger">{error}</Callout>;
  }
  if (!list) {
    return <div className="glass h-48 animate-pulse" aria-busy="true" aria-label="Loading change history" />;
  }
  if (list.total === 0) {
    return (
      <EmptyState
        icon={<History className="h-5 w-5" aria-hidden="true" />}
        title="No changes recorded yet"
        description="Every save of this monitor's configuration is recorded here. Its first save also keeps the configuration it has now, so that change can be undone."
      />
    );
  }

  const restoreChanges = restoreDiff?.changes.length ?? 0;

  return (
    <div className="space-y-4">
      <Callout tone="info">
        History starts at{' '}
        <span title={formatDateTimeFull(list.history_starts_at)}>{formatDateTime(list.history_starts_at)}</span>
        {list.truncated ? '; older versions were pruned' : ''}. Each monitor keeps its last {list.retention_limit}{' '}
        versions. Secret values are never recorded, shown or restored.
      </Callout>

      {reenter && (
        <Callout tone="warning" title={`Secrets to enter again after restoring version ${reenter.version}`}>
          Version {reenter.version} had {reenter.secrets.map(versionFieldLabel).join(', ')} set; the monitor no longer
          holds {reenter.secrets.length === 1 ? 'it' : 'them'} and a restore never brings a secret back. Enter{' '}
          {reenter.secrets.length === 1 ? 'it' : 'them'} again in Settings.
        </Callout>
      )}

      <div className="grid gap-4 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
        <Panel title="Versions" subtitle={pluralize(list.total, 'version')}>
          <ul className="-mx-2 space-y-1" aria-label="Configuration versions">
            {items.map((item) => (
              <li key={item.version} className="flex items-center gap-1">
                <VersionRow
                  item={item}
                  latest={item.version === latest}
                  selected={item.version === selected}
                  onSelect={() => setSelected(item.version)}
                />
                {canRestore && item.version !== latest && (
                  <IconButton
                    size="sm"
                    icon={<RotateCcw aria-hidden="true" />}
                    label={`Restore version ${item.version}`}
                    onClick={() => void openRestore(item.version)}
                  />
                )}
              </li>
            ))}
          </ul>
          <div className="mt-3">
            <Pagination page={page} totalPages={totalPages} onPageChange={setPage} />
          </div>
        </Panel>

        {current && (
          <Panel
            title={`Version ${current.version}`}
            subtitle={`${VERSION_SOURCE_LABEL[current.source] ?? current.source} · ${versionActorLabel(current)} · ${formatDateTimeFull(current.created_at)}`}
          >
            <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
              <SegmentedControl options={COMPARE_OPTIONS} value={compare} onChange={setCompare} label="Compare with" size="xs" />
              {current.changed_secret_fields.length > 0 && (
                <span className="inline-flex items-center gap-1.5 text-xs text-slate-400">
                  <KeyRound className="h-3.5 w-3.5" aria-hidden="true" />
                  Secrets set or changed: {current.changed_secret_fields.map(versionFieldLabel).join(', ')}
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
        open={restoreTarget !== null}
        tone="default"
        title={`Restore version ${restoreTarget ?? ''}?`}
        description={
          <>
            {restoreDiff
              ? restoreChanges === 0
                ? 'The monitor already matches this version.'
                : `This changes ${pluralize(restoreChanges, 'field')} of the live monitor (compare with Current to review them).`
              : 'This re-applies the version to the live monitor.'}{' '}
            Secrets keep their current values: a restore never brings back or removes a password, token or secret
            header, and lists any the version had that must be entered again. Pause state is left as it is, and
            channels, locations or monitors that no longer exist are left out. The restore is recorded as a new
            version.
          </>
        }
        confirmLabel="Restore"
        loading={restoring}
        onConfirm={() => void handleRestore()}
        onCancel={() => !restoring && setRestoreTarget(null)}
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
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={selected ? 'true' : undefined}
      className={`min-w-0 flex-1 rounded-lg px-3 py-2.5 text-left transition-colors ${FOCUS_RING_INSET} ${
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
        {versionActorLabel(item)} · {summarizeVersion(item)}
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
        {compare === 'current' ? 'The live monitor matches this version.' : 'Nothing differs from the previous version.'}
      </p>
    );
  }
  // "previous" reads previous → this version; "current" reads the live
  // monitor → this version, which is what a restore would write.
  const beforeLabel = compare === 'current' ? 'Live now' : diff.against ? `v${diff.against}` : 'Before';
  const afterLabel = compare === 'current' ? 'After restore' : `v${diff.version}`;
  return (
    <div className="glass-well overflow-x-auto">
      <table className="w-full text-left text-xs">
        <thead className="text-2xs uppercase tracking-wide text-slate-500">
          <tr>
            <th scope="col" className="px-3 py-2 font-medium">
              Field
            </th>
            <th scope="col" className="px-3 py-2 font-medium">
              {beforeLabel}
            </th>
            <th scope="col" className="px-3 py-2 font-medium">
              {afterLabel}
            </th>
          </tr>
        </thead>
        <tbody>
          {diff.changes.map((change) => {
            const secret = change.secret ? secretChangeText(change) : null;
            return (
              <tr key={change.path} className="border-t border-divider align-top">
                <th scope="row" className="px-3 py-2 font-medium text-slate-300">
                  <span className="inline-flex items-center gap-1.5">
                    {change.secret && <KeyRound className="h-3 w-3 text-slate-500" aria-label="Secret" />}
                    {versionFieldLabel(change.path)}
                  </span>
                </th>
                <td className="break-all px-3 py-2 font-mono text-rose-300">
                  {secret ? (
                    <span className="font-sans italic text-slate-400">{secret.before}</span>
                  ) : change.before === undefined ? (
                    <span className="text-slate-600">—</span>
                  ) : (
                    formatVersionValue(change.before, names)
                  )}
                </td>
                <td className="break-all px-3 py-2 font-mono text-emerald-300">
                  {secret ? (
                    <span className="font-sans italic text-slate-400">{secret.after}</span>
                  ) : change.after === undefined ? (
                    <span className="text-slate-600">—</span>
                  ) : (
                    formatVersionValue(change.after, names)
                  )}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
