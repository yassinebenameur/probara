'use client';

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import { ArrowDownLeft, ArrowUpRight, Pencil, Workflow } from 'lucide-react';
import { DependencyMonitor, Monitor } from '@/lib/types';
import {
  getMonitorDependencies,
  getMonitorDependents,
  getMonitors,
  updateMonitor,
} from '@/lib/api';
import { monitorStateColors } from '@/lib/monitor-utils';
import { useToast } from '@/components/ui/ToastProvider';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import ModalPortal from '@/components/ui/ModalPortal';
import { MonitorMultiSelect } from './MonitorMultiSelect';
import { monitorTypeLabel } from '@/lib/monitor-type-labels';

interface MonitorDependenciesCardProps {
  monitorId: string;
  monitorType?: string;
}

function apiErrorMessage(err: unknown): string {
  if (err && typeof err === 'object' && 'message' in err) {
    return String((err as { message?: unknown }).message || 'Something went wrong');
  }
  return err instanceof Error ? err.message : 'Something went wrong';
}

function DependencyRow({ item }: { item: DependencyMonitor }) {
  const colors = monitorStateColors(item.current_state);
  return (
    <Link
      href={`/monitors/${item.id}`}
      className="flex items-center gap-2.5 rounded-lg px-2 py-1.5 transition-colors hover:bg-well"
    >
      <span className={`h-2 w-2 shrink-0 rounded-full ${colors.dot}`} />
      <span className="min-w-0 flex-1 truncate text-sm text-slate-200">{item.name}</span>
      <span className="text-xs text-slate-500">{monitorTypeLabel(item.type)}</span>
    </Link>
  );
}

// Modal for editing a monitor's upstream dependencies without the full
// settings form round-trip. Saves via the monitor PATCH payload so cycle
// validation (409) is enforced server-side.
function EditDependenciesModal({
  monitorId,
  initialIds,
  onClose,
  onSaved,
}: {
  monitorId: string;
  initialIds: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { showToast } = useToast();
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [selected, setSelected] = useState<string[]>(initialIds);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    getMonitors({ page_size: 100 })
      .then((res) => setMonitors(res.items || []))
      .catch(() => setMonitors([]));
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  const save = async () => {
    try {
      setSaving(true);
      setError('');
      await updateMonitor(monitorId, { depends_on_ids: selected });
      showToast('Dependencies updated', 'success', 2500);
      onSaved();
      onClose();
    } catch (err) {
      setError(apiErrorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <ModalPortal>
      <div
        className="glass-scrim fixed inset-0 z-50 flex items-center justify-center p-4"
        onMouseDown={(e) => {
          if (e.target === e.currentTarget) onClose();
        }}
        role="dialog"
        aria-label="Edit dependencies"
      >
        <div className="glass-overlay w-full max-w-lg">
          <div className="px-6 pt-5">
            <h2 className="text-base font-semibold text-white">Edit dependencies</h2>
            <p className="mt-1.5 text-sm leading-relaxed text-slate-400">
              Pick the upstream monitors this one depends on. When one of them is down, alerts for
              this monitor are annotated with the likely root cause.
            </p>
          </div>

          <div className="space-y-3 px-6 py-4">
            <MonitorMultiSelect
              monitors={monitors}
              selectedIds={selected}
              onChange={setSelected}
              excludeIds={[monitorId]}
            />
            {error && <Callout tone="danger">{error}</Callout>}
          </div>

          <div className="flex justify-end gap-2 px-6 pb-5">
            <Button variant="ghost" size="sm" type="button" onClick={onClose} disabled={saving}>
              Cancel
            </Button>
            <Button variant="accent" size="sm" type="button" onClick={save} loading={saving} disabled={saving}>
              {saving ? 'Saving…' : 'Save dependencies'}
            </Button>
          </div>
        </div>
      </div>
    </ModalPortal>
  );
}

// Sidebar card on the monitor detail page: upstream dependencies and
// downstream dependents with live state dots, plus inline editing.
export function MonitorDependenciesCard({ monitorId, monitorType }: MonitorDependenciesCardProps) {
  const [upstream, setUpstream] = useState<DependencyMonitor[]>([]);
  const [downstream, setDownstream] = useState<DependencyMonitor[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [editing, setEditing] = useState(false);

  const load = useCallback(() => {
    let cancelled = false;
    Promise.all([getMonitorDependencies(monitorId), getMonitorDependents(monitorId)])
      .then(([deps, dependents]) => {
        if (cancelled) return;
        setUpstream(deps.items || []);
        setDownstream(dependents.items || []);
        setLoaded(true);
      })
      .catch(() => {
        if (!cancelled) setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
  }, [monitorId]);

  useEffect(() => load(), [load]);

  // Group rollups don't declare dependencies in v1.
  if (monitorType === 'group' || !loaded) {
    return null;
  }

  const empty = upstream.length === 0 && downstream.length === 0;

  return (
    <Panel
      title="Dependencies"
      actions={
        <Button
          type="button"
          variant="subtle"
          size="xs"
          className="-mr-2.5"
          icon={<Pencil className="h-3.5 w-3.5" strokeWidth={1.75} />}
          onClick={() => setEditing(true)}
          title="Edit dependencies"
        >
          Edit
        </Button>
      }
    >
      <div className="space-y-4">
        {empty && (
          <p className="text-ui text-slate-400">
            No dependencies yet. Link the services this monitor relies on so alerts can point at
            the likely root cause.
          </p>
        )}
        {upstream.length > 0 && (
          <div>
            <p className="mb-1 flex items-center gap-1.5 text-xs text-slate-500">
              <ArrowUpRight className="h-3.5 w-3.5" strokeWidth={1.75} />
              Depends on
            </p>
            <div className="-mx-2 space-y-0.5">
              {upstream.map((item) => (
                <DependencyRow key={item.id} item={item} />
              ))}
            </div>
          </div>
        )}
        {downstream.length > 0 && (
          <div>
            <p className="mb-1 flex items-center gap-1.5 text-xs text-slate-500">
              <ArrowDownLeft className="h-3.5 w-3.5" strokeWidth={1.75} />
              Depended on by
            </p>
            <div className="-mx-2 space-y-0.5">
              {downstream.map((item) => (
                <DependencyRow key={item.id} item={item} />
              ))}
            </div>
          </div>
        )}
        <Link
          href="/dependencies"
          className="inline-flex items-center gap-1.5 text-ui font-medium text-cyan-300 transition-colors hover:text-cyan-200"
        >
          <Workflow className="h-4 w-4" strokeWidth={1.75} />
          View dependency graph
        </Link>
      </div>

      {editing && (
        <EditDependenciesModal
          monitorId={monitorId}
          initialIds={upstream.map((u) => u.id)}
          onClose={() => setEditing(false)}
          onSaved={load}
        />
      )}
    </Panel>
  );
}
