'use client';

import type { MonitorLocationStatus } from '@/lib/types';
import { monitorStateColors } from '@/lib/monitor-utils';
import { formatLatency } from '@/lib/format';
import { RelativeTime } from '@/components/ui/RelativeTime';

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
    <div className="glass flex flex-wrap items-center gap-2 px-5 py-4">
      {locations.map((loc) => {
        const colors = monitorStateColors(loc.current_state);
        const hasLatency = typeof loc.last_latency_ms === 'number';
        // Latency is a success measure: a failing location's last duration
        // is how long the failure took (often the timeout), so it moves to
        // the state's tooltip instead of reading as response time.
        const showLatency = hasLatency && loc.current_state === 'up';
        return (
          <div key={loc.id} className="glass-well flex min-w-0 max-w-full items-center gap-2 px-3 py-1.5">
            <span className={`h-2 w-2 flex-shrink-0 rounded-full ${colors.dot}`} aria-hidden="true" />
            <span className="min-w-0 truncate text-sm font-medium text-white" title={loc.name}>
              {loc.name}
            </span>
            <span
              className="shrink-0 whitespace-nowrap text-xs text-slate-400"
              title={hasLatency && !showLatency ? `Last check failed after ${formatLatency(loc.last_latency_ms)}` : undefined}
            >
              {stateLabel(loc.current_state)}
            </span>
            {showLatency && (
              <span className="shrink-0 whitespace-nowrap text-xs tabular-nums text-slate-500">
                {formatLatency(loc.last_latency_ms)}
              </span>
            )}
            {loc.last_check_at && (
              <RelativeTime
                value={loc.last_check_at}
                className="shrink-0 whitespace-nowrap text-xs tabular-nums text-slate-500"
              />
            )}
            {!loc.connected && (
              <span className="shrink-0 whitespace-nowrap text-xs text-amber-300">worker offline</span>
            )}
          </div>
        );
      })}
      <span className="text-xs text-slate-500 sm:ml-auto">
        Down when ≥ {quorum} of {locations.length} locations fail
      </span>
    </div>
  );
}
