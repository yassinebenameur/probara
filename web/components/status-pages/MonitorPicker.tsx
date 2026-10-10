'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { Check, Layers, Loader2, Plus, Search } from 'lucide-react';
import type { Monitor } from '@/lib/types';
import Button from '@/components/ui/Button';
import { monitorMatches, sortMonitors, type DerivedState } from './useStatusPageFormState';
import TypeBadge from '@/components/ui/TypeBadge';
import { MONITOR_STATUS_DOT } from '@/lib/status-tones';
import { leafComponentStatus } from '@/lib/status-page-status';

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
      <Button
        type="button"
        variant="subtle"
        size="xs"
        icon={<Plus strokeWidth={1.75} />}
        onClick={() => setOpen((value) => !value)}
        aria-expanded={open}
        aria-haspopup="listbox"
      >
        Add monitors
      </Button>

      {open && (
        // Opaque: inside the section's .glass card the backdrop blur only
        // samples that card, so the sticky save bar below would show through.
        <div className="glass-overlay absolute left-0 z-30 mt-2 w-[min(400px,85vw)] overflow-hidden !rounded-xl bg-[rgb(var(--panel-rgb))]">
          <div className="flex items-center gap-2 border-b border-divider px-3 py-2.5">
            <Search className="h-4 w-4 flex-shrink-0 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
            <input
              ref={inputRef}
              type="search"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              placeholder="Search by name, URL, tag…"
              className="w-full bg-transparent text-sm text-slate-200 placeholder:text-slate-500 focus:outline-none"
            />
            {addableIds.length > 0 && (
              <button
                type="button"
                onClick={() => onAdd(addableIds)}
                className="flex-shrink-0 whitespace-nowrap text-xs font-medium text-cyan-300 transition-colors hover:text-cyan-200"
              >
                Add all{search.trim() ? ` (${addableIds.length})` : ''}
              </button>
            )}
          </div>

          <div className="max-h-72 overflow-y-auto p-1" role="listbox" aria-multiselectable="true">
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
                    className={`flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-colors hover:bg-well-hover ${
                      inThisSection ? 'bg-well' : ''
                    }`}
                  >
                    <span
                      aria-hidden="true"
                      className={`flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-[5px] border transition-colors ${
                        inThisSection
                          ? 'border-cyan-500 bg-cyan-500 text-onaccent'
                          : 'border-edge-control bg-control'
                      }`}
                    >
                      {inThisSection && <Check className="h-2.5 w-2.5" strokeWidth={3} />}
                    </span>
                    {monitor.type === 'group' ? (
                      <Layers
                        className="h-3.5 w-3.5 flex-shrink-0 text-slate-400"
                        strokeWidth={1.75}
                        aria-hidden="true"
                      />
                    ) : (
                      <span
                        aria-hidden="true"
                        className={`h-2 w-2 flex-shrink-0 rounded-full ${
                          MONITOR_STATUS_DOT[monitor.enabled ? leafComponentStatus(monitor) : 'paused']
                        }`}
                      />
                    )}
                    <span className="min-w-0 flex-1 truncate text-sm text-slate-200">
                      {monitor.name}
                    </span>
                    {elsewhere ? (
                      <span className="flex-shrink-0 text-xs text-amber-300">
                        in {placement.sectionTitle || 'another section'}
                      </span>
                    ) : (
                      <TypeBadge type={monitor.type} />
                    )}
                  </button>
                );
              })
            )}
            {hidden > 0 && (
              <p className="px-3 py-2 text-center text-xs text-slate-500">
                {hidden} more — refine your search to see them
              </p>
            )}
          </div>

          <div className="flex items-center justify-between border-t border-divider py-1.5 pl-3 pr-1.5">
            <span className="text-xs text-slate-500">Changes apply instantly</span>
            <Button
              type="button"
              variant="subtle"
              size="xs"
              onClick={() => {
                setOpen(false);
                setSearch('');
              }}
            >
              Done
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
