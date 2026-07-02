'use client';

import type { MonitorLocationStatus } from '@/lib/types';
import { formatTimeAgo, monitorStateColors } from '@/lib/monitor-utils';

interface MonitorLocationStripProps {
  locations: MonitorLocationStatus[];
  quorum: number;
}

function stateLabel(state: MonitorLocationStatus['current_state']): string {
  switch (state) {
    case 'up':
      return 'Up';
    case 'suspect':
      return 'Suspect';
    case 'down':
      return 'Down';
    default:
      return 'Unknown';
  }
}

// Per-location status chips for multi-location monitors.
export function MonitorLocationStrip({ locations, quorum }: MonitorLocationStripProps) {
  if (locations.length === 0) return null;

  return (
    <div className="rounded-xl border border-white/[0.06] bg-slate-900/50 px-4 py-3">
      <div className="flex flex-wrap items-center gap-2">
        {locations.map((loc) => {
          const colors = monitorStateColors(loc.current_state);
          return (
            <div
              key={loc.id}
              className={`flex items-center gap-2 rounded-lg border ${colors.border} bg-slate-900/60 px-3 py-1.5`}
            >
              <span className={`h-2 w-2 flex-shrink-0 rounded-full ${colors.dot}`} aria-hidden="true" />
              <span className="text-sm font-medium text-white">{loc.name}</span>
              <span className={`text-xs ${colors.text}`}>{stateLabel(loc.current_state)}</span>
              <span className="text-xs text-slate-500">
                {typeof loc.last_latency_ms === 'number' ? `${loc.last_latency_ms}ms` : '—'}
                {loc.last_check_at ? ` · ${formatTimeAgo(loc.last_check_at)}` : ''}
              </span>
              {!loc.connected && (
                <span className="text-xs text-amber-400">worker offline</span>
              )}
            </div>
          );
        })}
        <span className="ml-auto text-xs text-slate-500">
          Down when ≥ {quorum} of {locations.length} locations fail
        </span>
      </div>
    </div>
  );
}
