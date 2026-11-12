import type { MetricQueryRequest, MetricQueryResponse, MetricSeriesListResponse } from '../types';
import { apiRequest } from './core';

// Agent metric store API functions

export async function getMonitorMetricSeries(
  monitorId: string
): Promise<MetricSeriesListResponse> {
  return apiRequest<MetricSeriesListResponse>('GET', `/v1/monitors/${monitorId}/metrics/series`);
}

// Batch range query (read-only; viewers allowed). Buckets with no samples are
// omitted from the response — callers insert gap rows client-side.
export async function queryMonitorMetrics(
  monitorId: string,
  request: MetricQueryRequest
): Promise<MetricQueryResponse> {
  return apiRequest<MetricQueryResponse>('POST', `/v1/monitors/${monitorId}/metrics/query`, request);
}
