'use client';

import type { Location } from '@/lib/types';
import type { SyntheticTestPhase } from '@/lib/monitor-form/synthetic-live-test';
import { BTN_GHOST_SM } from './MonitorFormControls';
import type { SyntheticLiveTest } from './useSyntheticLiveTest';

/** What a synthetic editor needs to offer a realtime test of the saved monitor. */
export interface SyntheticLiveTestTarget {
  /** Unset until the monitor is saved; the test is disabled until then. */
  monitorId?: string;
  locationIds: string[];
  locations: Location[];
}

const phaseClasses = (phase: SyntheticTestPhase) => {
  switch (phase) {
    case 'success':
      return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300';
    case 'failure':
      return 'border-rose-500/30 bg-rose-500/10 text-rose-300';
    case 'error':
    case 'timeout':
      return 'border-amber-500/30 bg-amber-500/10 text-amber-300';
    case 'queueing':
    case 'polling':
      return 'border-cyan-500/30 bg-cyan-500/10 text-cyan-300';
    default:
      return 'border-white/[0.12] bg-slate-900/50 text-slate-300';
  }
};

export default function SyntheticLiveTestPanel({ test, target }: { test: SyntheticLiveTest; target: SyntheticLiveTestTarget }) {
  const isSaved = Boolean(target.monitorId);
  const { state } = test;

  return (
    <div className="rounded-lg border border-white/[0.08] bg-slate-900/40 p-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="text-sm font-medium text-white">Realtime Test</p>
          <p className="text-xs text-slate-500">Runs the saved monitor immediately and streams the latest result state here.</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {target.locationIds.length > 0 && (
            <select
              aria-label="Run test from location"
              value={test.locationId}
              onChange={(e) => test.setLocationId(e.target.value)}
              className="rounded-md border border-white/[0.08] bg-slate-900 px-2 py-1.5 text-xs text-slate-200"
            >
              <option value="">Run from: all locations</option>
              {target.locations
                .filter((loc) => target.locationIds.includes(loc.id))
                .map((loc) => (
                  <option key={loc.id} value={loc.id}>Run from: {loc.name}</option>
                ))}
            </select>
          )}
          <button type="button" className={BTN_GHOST_SM} onClick={test.start} disabled={!isSaved || test.isRunning}>
            {test.isRunning ? 'Testing...' : 'Test now'}
          </button>
        </div>
      </div>
      {!isSaved && <p className="mt-2 text-xs text-amber-400">Save this monitor first to enable realtime tests.</p>}
      {isSaved && <p className="mt-2 text-xs text-slate-500">Test runs use the currently saved monitor config.</p>}
      {state.phase !== 'idle' && (
        <div className={`mt-3 rounded-lg border px-3 py-2 text-xs ${phaseClasses(state.phase)}`}>
          <p>{state.message || 'Running...'}</p>
          {state.result && (
            <p className="mt-1">
              Status: <span className="font-medium uppercase">{state.result.status}</span>
              {typeof state.result.latency_ms === 'number' ? ` · ${state.result.latency_ms}ms` : ''}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
