'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { Plus, Workflow } from 'lucide-react';
import { DependencyMonitor, MonitorState } from '@/lib/types';
import { getMonitors } from '@/lib/api';
import Button from '@/components/ui/Button';
import { FOCUS_RING_INSET } from '@/components/ui/focus';
import { stateStyle, TYPE_ICONS } from './graphTheme';

// Toolbar popover: search any monitor and drop it onto the canvas so it can
// be wired up, even if it has no dependencies yet.
export function AddMonitorPicker({
  existingIds,
  onAdd,
  variant = 'ghost',
  align = 'right',
}: {
  existingIds: Set<string>;
  onAdd: (monitor: DependencyMonitor) => void;
  /** `accent` when it is the empty canvas's one next step. */
  variant?: 'ghost' | 'accent';
  /** Popover anchor: under the right edge (toolbar) or centred (empty state). */
  align?: 'right' | 'center';
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [allMonitors, setAllMonitors] = useState<DependencyMonitor[] | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open || allMonitors !== null) return;
    let cancelled = false;
    (async () => {
      const all: DependencyMonitor[] = [];
      for (let page = 1; page <= 10; page++) {
        const res = await getMonitors({ page, page_size: 100 });
        const items = res.items || [];
        all.push(
          // Group rollups don't declare dependencies in v1.
          ...items
            .filter((m) => m.type !== 'group')
            .map((m) => ({
              id: m.id,
              name: m.name,
              type: m.type,
              current_state: (m.current_state || 'unknown') as MonitorState,
            }))
        );
        if (items.length < 100 || all.length >= (res.total ?? 0)) break;
      }
      if (!cancelled) setAllMonitors(all);
    })().catch(() => {
      if (!cancelled) setAllMonitors([]);
    });
    return () => {
      cancelled = true;
    };
  }, [open, allMonitors]);

  useEffect(() => {
    if (!open) return;
    const onClickAway = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as globalThis.Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onClickAway);
    return () => document.removeEventListener('mousedown', onClickAway);
  }, [open]);

  const candidates = useMemo(() => {
    if (!allMonitors) return [];
    const q = query.trim().toLowerCase();
    return allMonitors
      .filter((m) => !existingIds.has(m.id))
      .filter((m) => !q || m.name.toLowerCase().includes(q) || m.type.toLowerCase().includes(q))
      .slice(0, 50);
  }, [allMonitors, existingIds, query]);

  return (
    <div className="relative" ref={rootRef}>
      <Button
        type="button"
        variant={variant}
        size={variant === 'accent' ? 'sm' : 'xs'}
        icon={<Plus strokeWidth={1.75} />}
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
      >
        Add monitor
      </Button>
      {open && (
        <div
          className={`glass-overlay absolute top-full z-20 mt-2 w-72 !rounded-xl p-1.5 text-left ${
            // pop-in animates transform, so the centred (translated) variant fades instead.
            align === 'center' ? 'left-1/2 -translate-x-1/2 animate-fade-in' : 'right-0 origin-top-right animate-pop-in'
          }`}
        >
          <input
            type="text"
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => e.key === 'Escape' && setOpen(false)}
            placeholder="Search monitors…"
            aria-label="Search monitors"
            className="input input-sm mb-1.5"
          />
          <div className="dashboard-scroll max-h-64 space-y-px overflow-y-auto">
            {allMonitors === null ? (
              <p role="status" className="px-2 py-3 text-center text-xs text-slate-500">Loading…</p>
            ) : candidates.length === 0 ? (
              <p className="px-2 py-3 text-center text-xs text-slate-500">
                {query ? 'No monitors match' : 'Everything is already on the canvas'}
              </p>
            ) : (
              candidates.map((m) => {
                const Icon = TYPE_ICONS[m.type] ?? Workflow;
                const s = stateStyle(m.current_state);
                return (
                  <button
                    key={m.id}
                    type="button"
                    onClick={() => {
                      onAdd(m);
                      setOpen(false);
                      setQuery('');
                    }}
                    className={`flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left transition-colors hover:bg-well-hover active:bg-control-hover ${FOCUS_RING_INSET}`}
                  >
                    <span className={`flex h-6 w-6 shrink-0 items-center justify-center rounded-md ${s.iconTile}`}>
                      <Icon className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
                    </span>
                    <span className="min-w-0 flex-1 truncate text-xs text-slate-200">{m.name}</span>
                    <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${s.dot}`} aria-hidden="true" />
                    <span className="sr-only">, {m.current_state ?? 'unknown'}</span>
                  </button>
                );
              })
            )}
          </div>
        </div>
      )}
    </div>
  );
}
