'use client';

import { useEffect, useState } from 'react';
import { createMaintenanceWindow, getGroupMembers, updateMaintenanceWindow } from '@/lib/api';
import type { MaintenanceWindow, Monitor } from '@/lib/types';
import { getAllMonitors, sortMonitorsByName } from '@/lib/monitor-list';
import Button from '@/components/ui/Button';
import ModalPortal from '@/components/ui/ModalPortal';
import { MonitorMultiSelect } from '@/components/monitors/MonitorMultiSelect';

interface MaintenanceWindowModalProps {
  window?: MaintenanceWindow; // present = edit mode
  onDone: () => void;
  onCancel: () => void;
}

// datetime-local inputs work in the browser's local timezone; the API speaks
// ISO/UTC. Convert at the edges.
function isoToLocalInput(iso: string): string {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function localInputToISO(value: string): string {
  return new Date(value).toISOString();
}

function defaultStart(): string {
  return isoToLocalInput(new Date().toISOString());
}

function defaultEnd(): string {
  return isoToLocalInput(new Date(Date.now() + 60 * 60 * 1000).toISOString());
}

export function MaintenanceWindowModal({ window, onDone, onCancel }: MaintenanceWindowModalProps) {
  const [title, setTitle] = useState(window?.title ?? '');
  const [description, setDescription] = useState(window?.description ?? '');
  const [startsAt, setStartsAt] = useState(window ? isoToLocalInput(window.starts_at) : defaultStart());
  const [endsAt, setEndsAt] = useState(window ? isoToLocalInput(window.ends_at) : defaultEnd());
  const [selectedIds, setSelectedIds] = useState<string[]>(window?.monitor_ids ?? []);
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [groupMembers, setGroupMembers] = useState<Record<string, string[]>>({});
  const [monitorsLoading, setMonitorsLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const all = sortMonitorsByName(await getAllMonitors());
        if (cancelled) return;
        setMonitors(all);

        // Membership isn't on the list payload, so pull it per group monitor —
        // it drives the by-group rows and the "covers N monitors" count.
        const groups = all.filter((m) => m.type === 'group');
        const memberEntries = await Promise.all(
          groups.map(async (group) => {
            try {
              const members = await getGroupMembers(group.id);
              return [group.id, members.map((m) => m.id)] as const;
            } catch (err) {
              console.error(`Failed to load members of group ${group.id}:`, err);
              return [group.id, []] as const;
            }
          })
        );
        if (!cancelled) setGroupMembers(Object.fromEntries(memberEntries));
      } catch (err) {
        console.error('Failed to load monitors:', err);
      } finally {
        if (!cancelled) setMonitorsLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  const canSave =
    !saving &&
    title.trim() !== '' &&
    startsAt !== '' &&
    endsAt !== '' &&
    new Date(endsAt) > new Date(startsAt) &&
    selectedIds.length > 0;

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const payload = {
        title: title.trim(),
        description,
        starts_at: localInputToISO(startsAt),
        ends_at: localInputToISO(endsAt),
        monitor_ids: selectedIds,
      };
      if (window) {
        await updateMaintenanceWindow(window.id, payload);
      } else {
        await createMaintenanceWindow(payload);
      }
      onDone();
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Operation failed';
      setError(message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <ModalPortal>
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 p-4 backdrop-blur-sm">
        <div className="dashboard-scroll max-h-[90vh] w-full max-w-xl overflow-y-auto rounded-xl border border-white/[0.08] bg-slate-900/95 p-6 shadow-2xl">
          {/* Header */}
          <div className="mb-5 flex items-start justify-between">
            <div>
              <h2 className="text-base font-semibold text-white">
                {window ? 'Edit maintenance window' : 'Schedule maintenance'}
              </h2>
              <p className="mt-1 text-xs text-slate-500">
                Alerts for the selected monitors are suppressed during the window. Checks keep running.
              </p>
            </div>
            <button
              onClick={onCancel}
              className="text-slate-400 hover:text-white transition-colors"
              aria-label="Close"
            >
              ×
            </button>
          </div>

          <div className="space-y-4">
            <div>
              <label className="mb-1 block text-xs font-medium text-slate-400" htmlFor="mw-title">
                Title
              </label>
              <input
                id="mw-title"
                type="text"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                placeholder="Database upgrade"
                className="input input-sm w-full"
              />
            </div>

            <div>
              <label className="mb-1 block text-xs font-medium text-slate-400" htmlFor="mw-description">
                Description <span className="text-slate-600">(shown on status pages)</span>
              </label>
              <textarea
                id="mw-description"
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                rows={2}
                placeholder="Optional details for status page viewers"
                className="input input-sm w-full resize-none"
              />
            </div>

            <div className="grid grid-cols-2 gap-3">
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-400" htmlFor="mw-start">
                  Starts
                </label>
                <input
                  id="mw-start"
                  type="datetime-local"
                  value={startsAt}
                  onChange={(e) => setStartsAt(e.target.value)}
                  className="input input-sm w-full"
                />
              </div>
              <div>
                <label className="mb-1 block text-xs font-medium text-slate-400" htmlFor="mw-end">
                  Ends
                </label>
                <input
                  id="mw-end"
                  type="datetime-local"
                  value={endsAt}
                  onChange={(e) => setEndsAt(e.target.value)}
                  className="input input-sm w-full"
                />
              </div>
            </div>
            {startsAt && endsAt && new Date(endsAt) <= new Date(startsAt) && (
              <p className="text-xs text-rose-400">End time must be after start time.</p>
            )}

            <div>
              <span className="mb-1 block text-xs font-medium text-slate-400">
                Monitors <span className="text-slate-600">— pick a group to cover its members</span>
              </span>
              {monitorsLoading ? (
                <div className="rounded-lg border border-white/[0.06] bg-slate-950/40 px-2 py-6">
                  <p className="text-center text-xs text-slate-500">Loading monitors…</p>
                </div>
              ) : (
                <MonitorMultiSelect
                  monitors={monitors}
                  selectedIds={selectedIds}
                  onChange={setSelectedIds}
                  groupMembers={groupMembers}
                  maxHeightClass="max-h-72"
                  emptyMessage="No monitors available"
                />
              )}
            </div>

            {error && <p className="text-xs text-rose-400">{error}</p>}

            <div className="flex justify-end gap-2 pt-1">
              <Button variant="ghost" size="sm" onClick={onCancel} disabled={saving}>
                Cancel
              </Button>
              <Button size="sm" onClick={save} disabled={!canSave} loading={saving}>
                {window ? 'Save changes' : 'Schedule'}
              </Button>
            </div>
          </div>
        </div>
      </div>
    </ModalPortal>
  );
}
