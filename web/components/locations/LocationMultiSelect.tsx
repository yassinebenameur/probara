'use client';

import { useMemo, useState } from 'react';
import { Search } from 'lucide-react';
import { Location } from '@/lib/types';
import Button from '@/components/ui/Button';
import Checkbox from '@/components/ui/Checkbox';

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
          aria-hidden="true"
        />
        <input
          type="text"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Search locations..."
          className="input !pl-9"
          aria-label="Search locations"
        />
      </div>

      <div className="flex items-center justify-between">
        <span className="text-xs text-slate-400" role="status">
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
                // The whole row is the checkbox's label (the label's last
                // child, the text column, stretches so the status sits right).
                <Checkbox
                  key={loc.id}
                  checked={isSelected}
                  onChange={() => toggle(loc.id)}
                  stretch
                  className={`w-full rounded-lg px-3 !py-2 transition-colors ${
                    isSelected ? 'bg-well-hover' : 'hover:bg-well'
                  }`}
                  label={
                    <span className="flex items-center gap-3">
                      <span className="min-w-0 flex-1">
                        <span className="block truncate font-medium text-white">{loc.name}</span>
                        <span className="block font-mono text-xs text-slate-500">{loc.slug}</span>
                      </span>
                      <span className="flex flex-shrink-0 items-center gap-1.5 text-xs text-slate-400">
                        <span
                          className={`h-2 w-2 rounded-full ${loc.connected ? 'bg-emerald-500' : 'bg-rose-500'}`}
                          aria-hidden="true"
                        />
                        {loc.connected ? 'Connected' : 'Disconnected'}
                      </span>
                    </span>
                  }
                />
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
