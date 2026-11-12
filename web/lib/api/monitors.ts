import type {
  AddMonitorsToGroupRequest,
  AgentInstallCommand,
  ChannelAssignment,
  CreateMonitorRequest,
  DBMetricsEnvelope,
  Monitor,
  MonitorListResponse,
  NotificationMode,
  PrometheusMetrics,
  PushInfo,
  RemoveMonitorsFromGroupRequest,
  RunMonitorNowResponse,
  SIPMetricsEnvelope,
  UpdateMonitorRequest,
} from '../types';
import { apiDownload, apiKeyHeaders, apiRequest, errorFromResponse, getApiUrl, withQuery } from './core';

export function getSyntheticBrowserScreenshotUrl(
  monitorId: string,
  tenantId: string,
  artifactPath: string
): string {
  return getApiUrl(
    withQuery(`/v1/monitors/${monitorId}/artifacts/screenshot`, { tenant_id: tenantId, path: artifactPath })
  );
}

// Monitor API functions
export async function getMonitors(params?: {
  tag?: string;
  enabled?: boolean;
  page?: number;
  page_size?: number;
}): Promise<MonitorListResponse> {
  return apiRequest<MonitorListResponse>(
    'GET',
    withQuery('/v1/monitors', {
      tag: params?.tag,
      enabled: params?.enabled,
      page: params?.page,
      page_size: params?.page_size,
    })
  );
}

export async function getMonitor(id: string): Promise<Monitor> {
  return apiRequest<Monitor>('GET', `/v1/monitors/${id}`);
}

export async function runMonitorNow(
  id: string,
  opts?: { location_id?: string }
): Promise<RunMonitorNowResponse> {
  return apiRequest<RunMonitorNowResponse>(
    'POST',
    `/v1/monitors/${id}/run`,
    opts?.location_id ? { location_id: opts.location_id } : undefined
  );
}

export async function createMonitor(data: CreateMonitorRequest): Promise<Monitor> {
  return apiRequest<Monitor>('POST', '/v1/monitors', data);
}

export interface TestMonitorConfigRequest {
  type: string;
  config: unknown;
  timeout_seconds?: number;
  // Resolves write-only "***" secret placeholders against the stored monitor
  // when testing an edit.
  monitor_id?: string;
  // Routes the ephemeral test to that private location's workers.
  location_id?: string;
}

export interface TestMonitorConfigResponse {
  status: 'success' | 'failure' | 'error';
  latency_ms?: number;
  error_message?: string;
  // Structured extras from the checker, same envelope as check results
  // (e.g. metrics_data.mongodb.unavailable for skipped cluster checks).
  metrics_data?: DBMetricsEnvelope & SIPMetricsEnvelope & { prometheus?: PrometheusMetrics };
}

// Runs one ephemeral check on a worker so a config can be validated before
// saving. Nothing is persisted.
export async function testMonitorConfig(data: TestMonitorConfigRequest): Promise<TestMonitorConfigResponse> {
  return apiRequest<TestMonitorConfigResponse>('POST', '/v1/monitors/test', data);
}

export async function updateMonitor(
  id: string,
  data: UpdateMonitorRequest
): Promise<Monitor> {
  return apiRequest<Monitor>('PATCH', `/v1/monitors/${id}`, data);
}

export async function deleteMonitor(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/monitors/${id}`);
}

export async function deleteMonitorHistory(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/monitors/${id}/history`);
}

export async function toggleMonitorEnabled(
  id: string,
  enabled: boolean
): Promise<Monitor> {
  return updateMonitor(id, { enabled });
}

// Group API functions
export async function addMonitorsToGroup(
  groupId: string,
  monitorIds: string[]
): Promise<void> {
  const data: AddMonitorsToGroupRequest = { monitor_ids: monitorIds };
  return apiRequest<void>('POST', `/v1/monitors/${groupId}/members`, data);
}

export async function removeMonitorsFromGroup(
  groupId: string,
  monitorIds: string[]
): Promise<void> {
  const data: RemoveMonitorsFromGroupRequest = { monitor_ids: monitorIds };
  return apiRequest<void>('DELETE', `/v1/monitors/${groupId}/members`, data);
}

export async function getGroupMembers(groupId: string): Promise<Monitor[]> {
  return apiRequest<Monitor[]>('GET', `/v1/monitors/${groupId}/members`);
}

// Agent API functions
export async function getAgentInstallCommand(
  monitorId: string,
  backendUrl?: string
): Promise<AgentInstallCommand> {
  const resolvedBackendUrl =
    backendUrl || (typeof window !== 'undefined' ? window.location.origin : undefined);
  return apiRequest<AgentInstallCommand>(
    'GET',
    withQuery(`/v1/monitors/${monitorId}/agent/install`, {
      backend_url: resolvedBackendUrl,
    })
  );
}

// Push API functions
export async function getPushInfo(
  monitorId: string,
  backendUrl?: string
): Promise<PushInfo> {
  const resolvedBackendUrl =
    backendUrl || (typeof window !== 'undefined' ? window.location.origin : undefined);
  return apiRequest<PushInfo>(
    'GET',
    withQuery(`/v1/monitors/${monitorId}/push/info`, {
      backend_url: resolvedBackendUrl,
    })
  );
}

// Bulk Alerting API functions
export async function bulkUpdateAlerting(data: {
  monitor_ids: string[];
  consecutive_failures_threshold?: number;
  notification_mode?: NotificationMode;
  notification_channels?: ChannelAssignment[];
}): Promise<{ updated: number }> {
  return apiRequest<{ updated: number }>('POST', '/v1/monitors/bulk/alerting', data);
}

export async function exportMonitors(): Promise<{ blob: Blob; filename: string }> {
  return apiDownload('/v1/monitors/export', 'monitors-export.yaml');
}

// The screenshot a synthetic browser check stored. The tenant rides in the
// URL, so this is a plain authenticated fetch: a failure only leaves the
// preview empty, it never signs the user out.
export async function getSyntheticBrowserScreenshot(
  monitorId: string,
  tenantId: string,
  artifactPath: string
): Promise<Blob> {
  const response = await fetch(getSyntheticBrowserScreenshotUrl(monitorId, tenantId, artifactPath), {
    method: 'GET',
    headers: apiKeyHeaders(),
    credentials: 'include',
  });
  if (!response.ok) {
    throw await errorFromResponse(response, `screenshot request failed: ${response.status}`);
  }
  return response.blob();
}
