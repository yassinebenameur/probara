'use client';

import { Monitor, MonitorResultsResponse, CheckResult } from '@/lib/types';
import { useSyntheticScreenshot } from '@/lib/useSyntheticScreenshot';

/**
 * A synthetic browser monitor's newest result that carries a failure
 * screenshot, and that screenshot as a blob URL.
 */
export function useLatestFailureScreenshot(monitor: Monitor | null, results: MonitorResultsResponse | null) {
  const getSyntheticBrowserScreenshotPath = (result?: CheckResult): string | null => {
    const metrics = result?.metrics_data as { synthetic_browser?: { artifacts?: { screenshot_path?: string } } } | undefined;
    return metrics?.synthetic_browser?.artifacts?.screenshot_path || null;
  };

  const latestSyntheticBrowserResult = monitor?.type === 'synthetic_browser'
    ? (results?.results || []).find((result) => Boolean(getSyntheticBrowserScreenshotPath(result)))
    : undefined;
  const latestSyntheticBrowserScreenshotPath = getSyntheticBrowserScreenshotPath(latestSyntheticBrowserResult);
  const { blobUrl: screenshotBlobURL, loading: screenshotLoading } = useSyntheticScreenshot(
    monitor?.id,
    monitor?.tenant_id,
    latestSyntheticBrowserScreenshotPath
  );

  return { latestSyntheticBrowserResult, screenshotBlobURL, screenshotLoading };
}
