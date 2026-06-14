'use client';

import { useEffect, useMemo, useState } from 'react';
import { createMaintenanceWindow, updateMaintenanceWindow } from '@/lib/api';
import type { MaintenanceWindow, Monitor } from '@/lib/types';
import { getAllMonitors, sortMonitorsByName } from '@/lib/monitor-list';
import Button from '@/components/ui/Button';

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
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set(window?.monitor_ids ?? []));
  const [monitors, setMonitors] = useState<Monitor[]>([]);
  const [monitorsLoading, setMonitorsLoading] = useState(true);
  const [search, setSearch] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const all = sortMonitorsByName(await getAllMonitors());
        if (!cancelled) setMonitors(all);
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

  const filteredMonitors = useMemo(() => {
    const term = search.trim().toLowerCase();
    if (!term) return monitors;
    return monitors.filter((m) => m.name.toLowerCase().includes(term));
  }, [monitors, search]);

  const toggleMonitor = (id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const canSave =
    !saving &&
    title.trim() !== '' &&
    startsAt !== '' &&
    endsAt !== '' &&
    new Date(endsAt) > new Date(startsAt) &&
    selectedIds.size > 0;

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      const payload = {
        title: title.trim(),
        description,
        starts_at: localInputToISO(startsAt),
        ends_at: localInputToISO(endsAt),
        monitor_ids: Array.from(selectedIds),
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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/70 backdrop-blur-sm">
      <div className="w-full max-w-lg rounded-xl border border-white/[0.08] bg-slate-900/95 p-6 shadow-2xl">
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
            <div className="mb-1 flex items-center justify-between">
              <span className="text-xs font-medium text-slate-400">
                Monitors <span className="text-slate-600">({selectedIds.size} selected)</span>
              </span>
            </div>
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search monitors…"
              className="input input-sm mb-2 w-full"
            />
            <div className="dashboard-scroll max-h-44 space-y-0.5 overflow-y-auto rounded-lg border border-white/[0.06] bg-slate-950/40 p-2">
              {monitorsLoading ? (
                <p className="px-2 py-3 text-center text-xs text-slate-500">Loading monitors…</p>
              ) : filteredMonitors.length === 0 ? (
                <p className="px-2 py-3 text-center text-xs text-slate-500">No monitors match.</p>
              ) : (
                filteredMonitors.map((m) => (
                  <label
                    key={m.id}
                    className="flex cursor-pointer items-center gap-2.5 rounded px-2 py-1.5 transition-colors hover:bg-white/[0.04] select-none"
                  >
                    <input
                      type="checkbox"
                      checked={selectedIds.has(m.id)}
                      onChange={() => toggleMonitor(m.id)}
                      className="h-3.5 w-3.5 rounded border-slate-600 bg-slate-800 accent-cyan-500 cursor-pointer flex-shrink-0"
                    />
                    <span className="min-w-0 truncate text-xs text-slate-300">{m.name}</span>
                    {m.type === 'group' && (
                      <span className="ml-auto flex-shrink-0 text-[9px] font-medium uppercase text-indigo-400">
                        Group — covers members
                      </span>
                    )}
                  </label>
                ))
              )}
            </div>
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
  );
}
