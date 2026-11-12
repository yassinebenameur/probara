import type {
  MonitorAnalyticsRange,
  MonitorAnalyticsResponse,
  MonitorMetricsSnapshotsResponse,
  MonitorResultsBatchResponse,
  MonitorResultsResponse,
} from '../types';
import { apiRequest, withQuery } from './core';

// Monitor Results API functions
export async function getMonitorResults(
  id: string,
  params?: { limit?: number; since?: string }
): Promise<MonitorResultsResponse> {
  return apiRequest<MonitorResultsResponse>(
    'GET',
    withQuery(`/v1/monitors/${id}/results`, {
      limit: params?.limit,
      since: params?.since,
    })
  );
}

// Monitor ids per GET /monitors/results/batch call (the server's cap; it
// keeps the URL well under proxy request-line limits).
export const MONITOR_RESULTS_BATCH_MAX_IDS = 100;

/** The newest `limit` results for up to MONITOR_RESULTS_BATCH_MAX_IDS
 * monitors, plus each group's direct member ids, in one request. */
export async function getMonitorResultsBatch(
  ids: string[],
  params?: { limit?: number }
): Promise<MonitorResultsBatchResponse> {
  return apiRequest<MonitorResultsBatchResponse>(
    'GET',
    withQuery('/v1/monitors/results/batch', { ids: ids.join(','), limit: params?.limit })
  );
}

export async function getMonitorAnalytics(
  id: string,
  params?: { range?: '1h' | '6h' | '24h' | '7d' | '30d' | '90d' | '365d' }
): Promise<MonitorAnalyticsResponse> {
  return apiRequest<MonitorAnalyticsResponse>(
    'GET',
    withQuery(`/v1/monitors/${id}/analytics`, {
      range: params?.range,
    })
  );
}

export async function getMonitorMetricsSnapshots(
  id: string,
  params?: { range?: MonitorAnalyticsRange }
): Promise<MonitorMetricsSnapshotsResponse> {
  return apiRequest<MonitorMetricsSnapshotsResponse>(
    'GET',
    withQuery(`/v1/monitors/${id}/metrics-snapshots`, {
      range: params?.range,
    })
  );
}
