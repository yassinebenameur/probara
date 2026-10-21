'use client';

import { useEffect, useId, useState } from 'react';
import Link from 'next/link';
import { getLocations } from '@/lib/api';
import type { Location } from '@/lib/types';
import { LocationMultiSelect } from '@/components/locations/LocationMultiSelect';

interface LocationsSectionProps {
  selectedIds: string[];
  onChange: (ids: string[]) => void;
  quorum: number;
  onQuorumChange: (n: number) => void;
  // Lets host forms reuse the fetched list (e.g. for a test-from-location picker).
  onLocationsLoaded?: (locations: Location[]) => void;
}

export function LocationsSection({
  selectedIds,
  onChange,
  quorum,
  onQuorumChange,
  onLocationsLoaded,
}: LocationsSectionProps) {
  const [locations, setLocations] = useState<Location[]>([]);
  const quorumId = useId();

  useEffect(() => {
    getLocations({ page_size: 100 })
      .then((res) => {
        const items = res.items || [];
        setLocations(items);
        onLocationsLoaded?.(items);
      })
      .catch(() => setLocations([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <div className="space-y-4">
      <p className="text-ui text-slate-400">
        Run this check from your private locations. None selected = the default platform fleet.
      </p>

      <LocationMultiSelect
        locations={locations}
        selectedIds={selectedIds}
        onChange={onChange}
        emptyMessage="No private locations yet"
      />

      {locations.length === 0 && (
        <p className="text-xs text-slate-500">
          Create one under{' '}
          <Link
            href="/locations"
            className="font-medium text-cyan-300 underline decoration-cyan-300/40 underline-offset-2 hover:text-cyan-200 hover:decoration-cyan-300"
          >
            Locations
          </Link>{' '}
          and deploy a worker there first.
        </p>
      )}

      {selectedIds.length >= 2 && (
        <div>
          <label htmlFor={quorumId} className="mb-1.5 block text-ui font-medium text-slate-300">
            Locations required down
          </label>
          <span className="flex items-center gap-2">
            <input
              id={quorumId}
              type="number"
              min={1}
              max={selectedIds.length}
              value={quorum}
              onChange={(e) => {
                const raw = Number(e.target.value);
                if (Number.isNaN(raw)) return;
                onQuorumChange(Math.min(Math.max(raw, 1), selectedIds.length));
              }}
              aria-describedby={`${quorumId}-hint`}
              className="input input-sm !w-24 tabular-nums"
            />
            <span className="text-xs text-slate-500">of {selectedIds.length}</span>
          </span>
          <p id={`${quorumId}-hint`} className="mt-1 text-xs text-slate-500">
            Down when at least this many locations fail; fewer failing = Degraded.
          </p>
        </div>
      )}
    </div>
  );
}
