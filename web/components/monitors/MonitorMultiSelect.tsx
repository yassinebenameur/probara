'use client';

import { useMemo, useState } from 'react';
import { Search } from 'lucide-react';
import { Monitor } from '@/lib/types';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';

interface MonitorMultiSelectProps {
  monitors: Monitor[];
  selectedIds: string[];
  onChange: (ids: string[]) => void;
  excludeIds?: string[];
  emptyMessage?: string;
}

// Searchable checkbox-list monitor picker, extracted from the GroupForm
// members pattern. Used for dependency selection (and reusable elsewhere).
export function MonitorMultiSelect({
  monitors,
  selectedIds,
  onChange,
  excludeIds = [],
  emptyMessage = 'No monitors available',
}: MonitorMultiSelectProps) {
  const [search, setSearch] = useState('');

  const selectable = useMemo(
    () => monitors.filter((m) => !excludeIds.includes(m.id)),
    [monitors, excludeIds]
  );

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return selectable;
    return selectable.filter(
      (m) => m.name.toLowerCase().includes(q) || m.type.toLowerCase().includes(q)
    );
  }, [selectable, search]);

  const toggle = (id: string) => {
    if (selectedIds.includes(id)) {
      onChange(selectedIds.filter((x) => x !== id));
    } else {
      onChange([...selectedIds, id]);
    }
  };

  const selectAllFiltered = () => {
    const merged = new Set([...selectedIds, ...filtered.map((m) => m.id)]);
    onChange(Array.from(merged));
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <div className="relative flex-1">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500" />
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search monitors..."
            className="input pl-9"
          />
        </div>
      </div>

      <div className="flex items-center justify-between">
        <span className="text-xs text-slate-400">
          {selectedIds.length} of {selectable.length} selected
        </span>
        <div className="flex gap-1.5">
          <Button variant="ghost" size="xs" type="button" onClick={selectAllFiltered}>
            Select all
          </Button>
          <Button variant="ghost" size="xs" type="button" onClick={() => onChange([])}>
            Clear
          </Button>
        </div>
      </div>

      <div className="max-h-56 overflow-y-auto rounded-lg border border-white/[0.06] bg-slate-900/40 p-2">
        {filtered.length === 0 ? (
          <p className="py-6 text-center text-sm text-slate-500">
            {selectable.length === 0 ? emptyMessage : 'No monitors match your search'}
          </p>
        ) : (
          <div className="space-y-1">
            {filtered.map((mon) => {
              const isSelected = selectedIds.includes(mon.id);
              return (
                <label
                  key={mon.id}
                  className={`flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 transition-colors ${
                    isSelected
                      ? 'border border-cyan-500/30 bg-cyan-500/10'
                      : 'border border-transparent hover:bg-white/[0.03]'
                  }`}
                >
                  <input
                    type="checkbox"
                    checked={isSelected}
                    onChange={() => toggle(mon.id)}
                    className="sr-only"
                  />
                  <div
                    className={`flex h-4 w-4 items-center justify-center rounded border transition-colors ${
                      isSelected ? 'border-cyan-500 bg-cyan-500' : 'border-slate-600'
                    }`}
                  >
                    {isSelected && (
                      <svg className="h-3 w-3 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                      </svg>
                    )}
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-white">{mon.name}</p>
                    <p className="text-xs text-slate-500">{mon.type.toUpperCase()}</p>
                  </div>
                  <Pill tone={mon.enabled ? 'success' : 'neutral'} size="xs" dot>
                    {mon.enabled ? 'Active' : 'Paused'}
                  </Pill>
                </label>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
