'use client';

import { useState } from 'react';
import { testMonitorConfig, type TestMonitorConfigResponse } from '@/lib/api';
import { errorMessage } from '@/lib/errors';

export type ConfigTestState =
  | { phase: 'idle' }
  | { phase: 'running' }
  | { phase: 'done'; result: TestMonitorConfigResponse }
  | { phase: 'request_failed'; message: string };

export type ConfigTest = ReturnType<typeof useConfigTest>;

/**
 * The form editors' "Test now" for types the worker can check from an unsaved
 * config (SIP, databases): one ephemeral check, optionally from one of the
 * monitor's locations. `done` carries the check's own verdict; `request_failed`
 * means the test never ran. Synthetic monitors run the saved monitor instead
 * (useSyntheticLiveTest).
 */
export function useConfigTest({
  monitorId,
  locationIds,
}: {
  /** The saved monitor, so "***" secret placeholders resolve against it. */
  monitorId?: string;
  locationIds: string[];
}) {
  const [state, setState] = useState<ConfigTestState>({ phase: 'idle' });
  // '' = run on the default fleet.
  const [pickedLocationId, setLocationId] = useState('');
  // Ignore a stale pick if the location was deselected since.
  const locationId = locationIds.includes(pickedLocationId) ? pickedLocationId : '';

  const run = async (request: { type: string; config: unknown; timeout_seconds: number }) => {
    setState({ phase: 'running' });
    try {
      const result = await testMonitorConfig({
        ...request,
        ...(monitorId ? { monitor_id: monitorId } : {}),
        ...(locationId ? { location_id: locationId } : {}),
      });
      setState({ phase: 'done', result });
    } catch (err) {
      setState({ phase: 'request_failed', message: errorMessage(err, 'Test request failed') });
    }
  };

  return { state, running: state.phase === 'running', locationId, setLocationId, run };
}
