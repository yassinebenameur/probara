'use client';

import { type Dispatch, type SetStateAction, useCallback, useEffect, useRef, useState } from 'react';
import { errorMessage } from './errors';

export interface AsyncDataState<T> {
  data: T;
  /** Patch what is on screen after a write (drop a deleted row, swap a saved one). */
  setData: Dispatch<SetStateAction<T>>;
  /** A load is in flight; true from the first render. */
  loading: boolean;
  /** The last load's failure message, '' when it succeeded. */
  error: string;
  /** Load again (after a write, or a retry). */
  reload: () => Promise<void>;
}

/**
 * The mount-time fetch a page or panel needs once: load, show a spinner, show
 * an error. Runs on mount and whenever `load` changes, so wrap `load` in
 * useCallback with the ids it depends on. A load that was superseded (newer
 * `load`, reload, unmount) never touches state.
 *
 * For data that should stay fresh (polling, alert-stream events) use
 * useLiveRefresh instead.
 */
export function useAsyncData<T>(load: () => Promise<T>, initial: T, fallbackError: string): AsyncDataState<T> {
  const [data, setData] = useState<T>(initial);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const sequence = useRef(0);
  const fallbackRef = useRef(fallbackError);
  useEffect(() => {
    fallbackRef.current = fallbackError;
  });

  const run = useCallback(async () => {
    const current = ++sequence.current;
    setLoading(true);
    setError('');
    try {
      const next = await load();
      if (current === sequence.current) setData(next);
    } catch (err) {
      if (current === sequence.current) setError(errorMessage(err, fallbackRef.current));
    } finally {
      if (current === sequence.current) setLoading(false);
    }
  }, [load]);

  useEffect(() => {
    void run();
    return () => {
      sequence.current += 1;
    };
  }, [run]);

  return { data, setData, loading, error, reload: run };
}
