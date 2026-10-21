'use client';

import type { MonitorLocationStatus } from '@/lib/types';
import { displayStatusFromState, isFailingStatus, monitorStateColors } from '@/lib/monitor-utils';
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

// Per-location status for multi-location monitors: one tile per location
// (state, last response time, last check), under a header that states the
// quorum rule, so a failing location reads against what it takes to go down.
export function MonitorLocationStrip({ locations, quorum }: MonitorLocationStripProps) {
  if (locations.length === 0) return null;

  const failing = locations.filter((loc) => isFailingStatus(displayStatusFromState(loc))).length;

  return (
    <section className="glass px-5 pb-4 pt-4" aria-labelledby="monitor-locations-title">
      <div className="mb-3 flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h3 id="monitor-locations-title" className="text-title font-semibold text-white">
          Locations
          <span className="ml-2 text-xs font-normal tabular-nums text-slate-500">
            {failing > 0 ? `${failing} of ${locations.length} failing` : `${locations.length} checking`}
          </span>
        </h3>
        <span className="text-xs tabular-nums text-slate-500">
          Down when ≥ {quorum} of {locations.length} locations fail
        </span>
      </div>
      <ul className="grid gap-2 [grid-template-columns:repeat(auto-fit,minmax(15rem,1fr))]">
        {locations.map((loc) => {
          const colors = monitorStateColors(loc.current_state);
          const hasLatency = typeof loc.last_latency_ms === 'number';
          // Latency is a success measure: a failing location's last duration
          // is how long the failure took (often the timeout), so it moves to
          // the state's tooltip instead of reading as response time.
          const showLatency = hasLatency && loc.current_state === 'up';
          return (
            <li key={loc.id} className="glass-well min-w-0 px-3.5 py-2.5">
              <div className="flex min-w-0 items-center gap-2">
                <span className={`h-2 w-2 shrink-0 rounded-full ${colors.dot}`} aria-hidden="true" />
                <span className="min-w-0 flex-1 truncate text-ui font-medium text-white" title={loc.name}>
                  {loc.name}
                </span>
                {showLatency && (
                  <span className="shrink-0 text-ui tabular-nums text-slate-200">{formatLatency(loc.last_latency_ms)}</span>
                )}
              </div>
              <div className="mt-0.5 flex items-center gap-2 pl-4 text-xs tabular-nums text-slate-500">
                <span
                  className={loc.current_state === 'up' ? 'text-slate-400' : colors.text}
                  title={hasLatency && !showLatency ? `Last check failed after ${formatLatency(loc.last_latency_ms)}` : undefined}
                >
                  {stateLabel(loc.current_state)}
                </span>
                {loc.last_check_at && <RelativeTime value={loc.last_check_at} className="truncate" />}
                {!loc.connected && <span className="ml-auto shrink-0 text-amber-300">worker offline</span>}
              </div>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
