'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { Check, Layers, Loader2, Plus, Search } from 'lucide-react';
import type { Monitor } from '@/lib/types';
import { monitorMatches, sortMonitors, type DerivedState } from './useStatusPageFormState';

interface MonitorPickerProps {
  monitors: Monitor[];
  loading: boolean;
  derived: DerivedState;
  sectionId: string;
  onAdd: (monitorIds: string[]) => void;
  onRemove: (monitorId: string) => void;
}

const VISIBLE_LIMIT = 60;

/**
 * "Add monitors" button + searchable popover. Checking a monitor puts it in
 * this section immediately (moving it from another section if needed);
 * unchecking removes it from the page. No staging, no confirm step.
 */
export default function MonitorPicker({
  monitors,
  loading,
  derived,
  sectionId,
  onAdd,
  onRemove,
}: MonitorPickerProps) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState('');
  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!open) return;
    const handlePointer = (event: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false);
        setSearch('');
      }
    };
    const handleKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setOpen(false);
        setSearch('');
      }
    };
    document.addEventListener('mousedown', handlePointer);
    document.addEventListener('keydown', handleKey);
    return () => {
      document.removeEventListener('mousedown', handlePointer);
      document.removeEventListener('keydown', handleKey);
    };
  }, [open]);

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  const filtered = useMemo(() => {
    const needle = search.trim().toLowerCase();
    return sortMonitors(monitors.filter((monitor) => monitorMatches(monitor, needle)));
  }, [monitors, search]);

  const visible = filtered.slice(0, VISIBLE_LIMIT);
  const hidden = filtered.length - visible.length;
  const addableIds = filtered
    .filter((m) => derived.monitorIdToSection.get(m.id)?.sectionId !== sectionId)
    .map((m) => m.id);

  return (
    <div ref={containerRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((value) => !value)}
        aria-expanded={open}
        aria-haspopup="listbox"
        className="flex items-center gap-1.5 rounded-lg border border-dashed border-white/[0.12] px-3 py-1.5 text-xs text-slate-400 transition-colors hover:border-cyan-500/40 hover:text-cyan-200"
      >
        <Plus className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
        Add monitors
      </button>

      {open && (
        <div className="absolute left-0 z-30 mt-2 w-[min(400px,85vw)] overflow-hidden rounded-xl border border-white/[0.1] bg-slate-900/95 shadow-2xl backdrop-blur">
          <div className="flex items-center gap-2 border-b border-white/[0.06] px-3 py-2.5">
            <Search className="h-3.5 w-3.5 flex-shrink-0 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
            <input
              ref={inputRef}
              type="search"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search by name, URL, tag…"
              className="w-full bg-transparent text-sm text-slate-200 placeholder:text-slate-600 focus:outline-none"
            />
            {addableIds.length > 0 && (
              <button
                type="button"
                onClick={() => onAdd(addableIds)}
                className="flex-shrink-0 whitespace-nowrap text-[11px] font-medium text-cyan-300 transition-colors hover:text-cyan-200"
              >
                Add all{search.trim() ? ` (${addableIds.length})` : ''}
              </button>
            )}
          </div>

          <div className="max-h-72 overflow-y-auto p-1.5" role="listbox" aria-multiselectable="true">
            {loading ? (
              <div className="flex items-center justify-center gap-2 py-8 text-xs text-slate-500">
                <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />
                Loading monitors…
              </div>
            ) : visible.length === 0 ? (
              <p className="px-3 py-6 text-center text-xs text-slate-500">
                {monitors.length === 0 ? 'No monitors in this workspace yet.' : 'No monitors match your search.'}
              </p>
            ) : (
              visible.map((monitor) => {
                const placement = derived.monitorIdToSection.get(monitor.id);
                const inThisSection = placement?.sectionId === sectionId;
                const elsewhere = placement && !inThisSection;
                return (
                  <button
                    key={monitor.id}
                    type="button"
                    role="option"
                    aria-selected={inThisSection}
                    onClick={() =>
                      inThisSection ? onRemove(monitor.id) : onAdd([monitor.id])
                    }
                    className={`flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-colors ${
                      inThisSection ? 'bg-cyan-500/[0.08]' : 'hover:bg-white/[0.04]'
                    }`}
                  >
                    <span
                      aria-hidden="true"
                      className={`flex h-4 w-4 flex-shrink-0 items-center justify-center rounded border transition-colors ${
                        inThisSection
                          ? 'border-cyan-400 bg-cyan-500 text-slate-950'
                          : 'border-white/[0.15] bg-slate-950/50'
                      }`}
                    >
                      {inThisSection && <Check className="h-3 w-3" strokeWidth={3} />}
                    </span>
                    {monitor.type === 'group' ? (
                      <Layers
                        className="h-3.5 w-3.5 flex-shrink-0 text-violet-300"
                        strokeWidth={1.75}
                        aria-hidden="true"
                      />
                    ) : (
                      <span
                        aria-hidden="true"
                        className={`h-1.5 w-1.5 flex-shrink-0 rounded-full ${
                          monitor.enabled ? 'bg-emerald-400' : 'bg-slate-600'
                        }`}
                      />
                    )}
                    <span className="min-w-0 flex-1 truncate text-sm text-slate-200">
                      {monitor.name}
                    </span>
                    <span className="flex-shrink-0 text-[10px] uppercase tracking-wider text-slate-600">
                      {elsewhere ? (
                        <span className="normal-case tracking-normal text-amber-300/80">
                          in {placement.sectionTitle || 'another section'}
                        </span>
                      ) : (
                        monitor.type
                      )}
                    </span>
                  </button>
                );
              })
            )}
            {hidden > 0 && (
              <p className="px-3 py-2 text-center text-[11px] text-slate-600">
                {hidden} more — refine your search to see them
              </p>
            )}
          </div>

          <div className="flex items-center justify-between border-t border-white/[0.06] px-3 py-2">
            <span className="text-[11px] text-slate-600">Changes apply instantly</span>
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                setSearch('');
              }}
              className="text-[11px] font-medium text-slate-400 transition-colors hover:text-slate-200"
            >
              Done
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
