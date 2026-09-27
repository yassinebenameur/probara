'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { getMonitorResults, runMonitorNow } from '@/lib/api';
import {
  type SyntheticLiveTestClient,
  type SyntheticTestState,
  IDLE_SYNTHETIC_TEST,
  isSyntheticTestRunning,
  runSyntheticLiveTest,
} from '@/lib/monitor-form/synthetic-live-test';

const apiClient: SyntheticLiveTestClient = { runNow: runMonitorNow, getResults: getMonitorResults };

export interface SyntheticLiveTest {
  state: SyntheticTestState;
  isRunning: boolean;
  /** '' = all of the monitor's locations (or the default fleet). */
  locationId: string;
  setLocationId: (id: string) => void;
  /** No-op for unsaved monitors or while a run is in flight. */
  start: () => void;
}

/**
 * Runs the saved monitor on demand and polls for its result. The in-flight run
 * is aborted on unmount and when a new run starts, so a late response from a
 * superseded run or an unmounted editor never reaches state.
 */
export function useSyntheticLiveTest({
  monitorId,
  locationIds,
}: {
  monitorId?: string;
  locationIds: string[];
}): SyntheticLiveTest {
  const [state, setState] = useState<SyntheticTestState>(IDLE_SYNTHETIC_TEST);
  const [pickedLocationId, setLocationId] = useState('');
  const runRef = useRef<AbortController | null>(null);

  useEffect(() => () => runRef.current?.abort(), []);

  // Ignore a stale pick if the location was deselected since.
  const locationId = locationIds.includes(pickedLocationId) ? pickedLocationId : '';
  const isRunning = isSyntheticTestRunning(state);

  const start = useCallback(() => {
    if (!monitorId || isRunning) return;
    runRef.current?.abort();
    const run = new AbortController();
    runRef.current = run;
    void runSyntheticLiveTest({ monitorId, locationId, client: apiClient, signal: run.signal, onUpdate: setState });
  }, [monitorId, isRunning, locationId]);

  return { state, isRunning, locationId, setLocationId, start };
}
