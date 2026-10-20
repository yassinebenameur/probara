'use client';

import { useEffect, useRef, useState } from 'react';
import { getMonitorResults, runMonitorNow } from '@/lib/api';
import { CheckResult, MonitorType } from '@/lib/types';
import { SyntheticTestPhase, SyntheticTestState } from '@/lib/monitor-form/synthetic';

const MAX_POLL_ATTEMPTS = 30;
const POLL_INTERVAL_MS = 2000;
// A result this much older than the queue time still counts (clock skew
// between the API and the worker that stamps created_at).
const RESULT_SKEW_MS = 1500;

export type SyntheticLiveTest = ReturnType<typeof useSyntheticLiveTest>;

/**
 * The synthetic editors' "Test now": queues an on-demand run of the saved
 * monitor (optionally from one of its locations), then polls its results
 * until one newer than the queue time arrives. A newer run, a type change or
 * unmounting bumps the sequence, so a stale poll stops without touching state.
 */
export function useSyntheticLiveTest({
  monitorId,
  monitorType,
  locationIds,
}: {
  monitorId?: string;
  monitorType: MonitorType;
  locationIds: string[];
}) {
  const [state, setState] = useState<SyntheticTestState>({ phase: 'idle' });
  // '' = fan out to all the monitor's locations (or default fleet when none).
  const [pickedLocationId, setLocationId] = useState('');
  const sequenceRef = useRef(0);
  const mountedRef = useRef(true);

  const isSynthetic = monitorType === 'synthetic_api' || monitorType === 'synthetic_browser';
  const running = state.phase === 'queueing' || state.phase === 'polling';
  // Ignore a stale pick if the location was deselected since.
  const locationId = locationIds.includes(pickedLocationId) ? pickedLocationId : '';

  useEffect(() => {
    // Set on (re)mount too: StrictMode's dev double-mount runs the cleanup once.
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      sequenceRef.current += 1;
    };
  }, []);

  useEffect(() => {
    setState({ phase: 'idle' });
    sequenceRef.current += 1;
  }, [monitorType]);

  const start = async () => {
    if (!monitorId || !isSynthetic || running) return;

    const chosenLocationId = locationId;
    const sequence = ++sequenceRef.current;
    const stale = () => !mountedRef.current || sequence !== sequenceRef.current;
    setState({
      phase: 'queueing',
      message: 'Queuing on-demand run...',
    });

    try {
      const runResponse = await runMonitorNow(
        monitorId,
        chosenLocationId ? { location_id: chosenLocationId } : undefined
      );
      if (stale()) return;

      const queuedAtISO = runResponse.queued_at || new Date().toISOString();
      const queuedAtMs = new Date(queuedAtISO).getTime();
      setState({
        phase: 'polling',
        message: 'Run queued. Waiting for worker result...',
        queuedAt: queuedAtISO,
        jobId: runResponse.job_id,
      });

      let matchedResult: CheckResult | null = null;

      for (let attempt = 1; attempt <= MAX_POLL_ATTEMPTS; attempt++) {
        if (stale()) return;

        const response = await getMonitorResults(monitorId, { limit: 10, since: queuedAtISO });
        const candidate = (response.results || []).find((result) => {
          if (chosenLocationId && result.location_id !== chosenLocationId) return false;
          const resultMs = new Date(result.created_at).getTime();
          return resultMs >= queuedAtMs - RESULT_SKEW_MS;
        });

        if (candidate) {
          matchedResult = candidate;
          break;
        }

        setState((prev) => ({
          ...prev,
          phase: 'polling',
          message: `Waiting for worker result... (${attempt}/${MAX_POLL_ATTEMPTS})`,
        }));

        await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
      }

      if (stale()) return;

      if (!matchedResult) {
        setState((prev) => ({
          ...prev,
          phase: 'timeout',
          message: 'No fresh result found yet. Check worker/scheduler health and try again.',
        }));
        return;
      }

      const finalPhase: SyntheticTestPhase = matchedResult.status === 'success' ? 'success' : 'failure';
      setState({
        phase: finalPhase,
        queuedAt: queuedAtISO,
        jobId: runResponse.job_id,
        result: matchedResult,
        message: matchedResult.error_message || (finalPhase === 'success' ? 'Run completed successfully.' : 'Run failed.'),
      });
    } catch (err: any) {
      if (stale()) return;
      setState({
        phase: 'error',
        message: err?.message || 'Failed to run monitor on demand.',
      });
    }
  };

  return { state, running, start, locationId, setLocationId };
}
