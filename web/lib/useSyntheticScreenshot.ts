'use client';

import { useEffect, useState } from 'react';
import { getSyntheticBrowserScreenshot } from './api';

/**
 * Loads a synthetic browser check's stored screenshot as an object URL (the
 * artifact route needs auth headers, so an <img src> cannot fetch it). Pass
 * `artifactPath: null` when there is nothing to show. The URL is revoked when
 * the target changes or the caller unmounts.
 */
export function useSyntheticScreenshot(
  monitorId: string | undefined,
  tenantId: string | undefined,
  artifactPath: string | null
): { blobUrl: string | null; loading: boolean } {
  const [blobUrl, setBlobUrl] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    let cancelled = false;
    let objectURL: string | null = null;

    const replace = (next: string | null) =>
      setBlobUrl((prev) => {
        if (prev) URL.revokeObjectURL(prev);
        return next;
      });

    if (!monitorId || !tenantId || !artifactPath) {
      replace(null);
      setLoading(false);
      return;
    }

    const load = async () => {
      setLoading(true);
      try {
        const blob = await getSyntheticBrowserScreenshot(monitorId, tenantId, artifactPath);
        objectURL = URL.createObjectURL(blob);
        if (!cancelled) replace(objectURL);
      } catch (err) {
        console.error('Failed to load synthetic browser screenshot:', err);
        if (!cancelled) replace(null);
      } finally {
        if (!cancelled) setLoading(false);
      }
    };

    load();

    return () => {
      cancelled = true;
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [monitorId, tenantId, artifactPath]);

  return { blobUrl, loading };
}
