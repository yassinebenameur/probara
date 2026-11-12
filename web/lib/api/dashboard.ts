import type {
  DashboardGroupSparklineResponse,
  DashboardProblemMonitorsResponse,
  DashboardRecentAlertsResponse,
  DashboardRecentFailuresResponse,
  DashboardSummaryResponse,
} from '../types';
import { apiRequest, withQuery } from './core';

export async function getDashboardSummary(params?: {
  range?: '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
  tags?: string[];
}): Promise<DashboardSummaryResponse> {
  return apiRequest<DashboardSummaryResponse>(
    'GET',
    withQuery('/v1/dashboard/summary', {
      range: params?.range,
      tag: params?.tags,
    })
  );
}

export async function getDashboardGroupSparkline(params: {
  group: string | null;
  range: '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
  tags?: string[];
}): Promise<DashboardGroupSparklineResponse> {
  return apiRequest<DashboardGroupSparklineResponse>(
    'GET',
    withQuery('/v1/dashboard/group-sparkline', {
      group: params.group,
      range: params.range,
      tag: params.tags,
    })
  );
}

export async function getDashboardProblemMonitors(params?: {
  range?: '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
  limit?: number;
  tags?: string[];
}): Promise<DashboardProblemMonitorsResponse> {
  return apiRequest<DashboardProblemMonitorsResponse>(
    'GET',
    withQuery('/v1/dashboard/problem-monitors', {
      range: params?.range,
      limit: params?.limit,
      tag: params?.tags,
    })
  );
}

export async function getDashboardRecentFailures(params?: {
  range?: '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
  limit?: number;
  tags?: string[];
}): Promise<DashboardRecentFailuresResponse> {
  return apiRequest<DashboardRecentFailuresResponse>(
    'GET',
    withQuery('/v1/dashboard/recent-failures', {
      range: params?.range,
      limit: params?.limit,
      tag: params?.tags,
    })
  );
}

export async function getDashboardRecentAlerts(params?: {
  range?: '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
  limit?: number;
  tags?: string[];
}): Promise<DashboardRecentAlertsResponse> {
  return apiRequest<DashboardRecentAlertsResponse>(
    'GET',
    withQuery('/v1/dashboard/recent-alerts', {
      range: params?.range,
      limit: params?.limit,
      tag: params?.tags,
    })
  );
}
