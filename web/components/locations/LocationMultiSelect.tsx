'use client';

import { useMemo, useState } from 'react';
import { Search } from 'lucide-react';
import { Location } from '@/lib/types';
import Button from '@/components/ui/Button';

interface LocationMultiSelectProps {
  locations: Location[];
  selectedIds: string[];
  onChange: (ids: string[]) => void;
  emptyMessage?: string;
}

// Searchable checkbox-list location picker, mirroring MonitorMultiSelect.
export function LocationMultiSelect({
  locations,
  selectedIds,
  onChange,
  emptyMessage = 'No locations available',
}: LocationMultiSelectProps) {
  const [search, setSearch] = useState('');

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return locations;
    return locations.filter(
      (l) => l.name.toLowerCase().includes(q) || l.slug.toLowerCase().includes(q)
    );
  }, [locations, search]);

  const toggle = (id: string) => {
    if (selectedIds.includes(id)) {
      onChange(selectedIds.filter((x) => x !== id));
    } else {
      onChange([...selectedIds, id]);
    }
  };

  const selectAllFiltered = () => {
    const merged = new Set([...selectedIds, ...filtered.map((l) => l.id)]);
    onChange(Array.from(merged));
  };

  return (
    <div className="space-y-2">
      <div className="relative">
        <Search
          className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-500"
          strokeWidth={1.75}
        />
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search locations..."
          className="input !pl-9"
        />
      </div>

      <div className="flex items-center justify-between">
        <span className="text-xs text-slate-400">
          {selectedIds.length} of {locations.length} selected
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

      <div className="glass-well max-h-56 overflow-y-auto p-1.5">
        {filtered.length === 0 ? (
          <p className="py-6 text-center text-sm text-slate-500">
            {locations.length === 0 ? emptyMessage : 'No locations match your search'}
          </p>
        ) : (
          <div className="space-y-1">
            {filtered.map((loc) => {
              const isSelected = selectedIds.includes(loc.id);
              return (
                <label
                  key={loc.id}
                  className={`flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 transition-colors ${
                    isSelected ? 'bg-well-hover' : 'hover:bg-well'
                  }`}
                >
                  <input
                    type="checkbox"
                    checked={isSelected}
                    onChange={() => toggle(loc.id)}
                    className="sr-only"
                  />
                  <div
                    className={`flex h-4 w-4 flex-shrink-0 items-center justify-center rounded-[5px] border transition-colors ${
                      isSelected ? 'border-cyan-500 bg-cyan-500' : 'border-edge-control bg-control'
                    }`}
                  >
                    {isSelected && (
                      <svg className="h-2.5 w-2.5 text-onaccent" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={3} d="M5 13l4 4L19 7" />
                      </svg>
                    )}
                  </div>
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-sm font-medium text-white">{loc.name}</p>
                    <p className="font-mono text-xs text-slate-500">{loc.slug}</p>
                  </div>
                  <span className="flex flex-shrink-0 items-center gap-1.5 text-xs text-slate-400">
                    <span
                      className={`h-2 w-2 rounded-full ${loc.connected ? 'bg-emerald-500' : 'bg-rose-500'}`}
                      aria-hidden="true"
                    />
                    {loc.connected ? 'Connected' : 'Disconnected'}
                  </span>
                </label>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
