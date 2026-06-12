import { getApiKey, clearApiKey } from './auth';
import { clearSelectedTenantId, getSelectedTenantId, setSelectedTenantId } from './tenant';
import type {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  MonitorListResponse,
  Alert,
  AlertListResponse,
  AlertChannel,
  CreateAlertChannelRequest,
  UpdateAlertChannelRequest,
  AlertChannelListResponse,
  StatusPage,
  CreateStatusPageRequest,
  UpdateStatusPageRequest,
  StatusPageListResponse,
  MonitorResultsResponse,
  MonitorAnalyticsResponse,
  DependencyGraph,
  DependencyMonitor,
  DashboardOverviewResponse,
  DashboardSummaryResponse,
  DashboardGroupSparklineResponse,
  DashboardProblemMonitorsResponse,
  DashboardRecentFailuresResponse,
  DashboardRecentAlertsResponse,
  RunMonitorNowResponse,
  AddMonitorsToGroupRequest,
  RemoveMonitorsFromGroupRequest,
  AgentInstallCommand,
  PushInfo,
  ApiError,
  ImportPreviewResponse,
  ImportExecuteRequest,
  ImportExecuteResponse,
  TenantListResponse,
  TenantSettings,
  UpdateTenantSettingsRequest,
  AdminUser,
  AdminUserListResponse,
  CreateAdminUserRequest,
  UpdateAdminUserRequest,
  ApiKey,
  ApiKeyListResponse,
  CreateApiKeyRequest,
  IncidentListResponse,
  IncidentDetail,
  CreateIncidentRequest,
  UpdateIncidentRequest,
  CreateIncidentTimelineEntryRequest,
  PublishIncidentToStatusPageRequest,
  IncidentState,
  PluginManifest,
  NotificationSettings,
  NotificationMode,
  ChannelAssignment,
} from './types';

const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || '/api';

function getApiUrl(path: string): string {
  // If API_BASE_URL is relative, use it as-is
  if (API_BASE_URL.startsWith('/')) {
    return `${API_BASE_URL}${path}`;
  }
  // If absolute, ensure path starts with /
  return `${API_BASE_URL}${path.startsWith('/') ? path : `/${path}`}`;
}

export function getSyntheticBrowserScreenshotUrl(
  monitorId: string,
  tenantId: string,
  artifactPath: string
): string {
  const query = new URLSearchParams({
    tenant_id: tenantId,
    path: artifactPath,
  });
  return getApiUrl(`/v1/monitors/${monitorId}/artifacts/screenshot?${query.toString()}`);
}

async function handleResponse<T>(response: Response, hasApiKey: boolean): Promise<T> {
  if (response.status === 401) {
    // Unauthorized - redirect based on auth mode
    if (hasApiKey) {
      clearApiKey();
      if (typeof window !== 'undefined') {
        window.location.href = '/connect';
      }
    } else if (typeof window !== 'undefined') {
      window.location.href = '/login';
    }
    throw new Error('Unauthorized');
  }

  const contentType = response.headers.get('content-type');
  if (!contentType || !contentType.includes('application/json')) {
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }
    return {} as T;
  }

  const data = await response.json();

  if (!response.ok) {
    const error: ApiError = data.error
      ? { error: data.error, message: data.message || data.error }
      : { error: 'unknown_error', message: data.message || 'An error occurred' };
    throw error;
  }

  return data;
}

const AUTH_PATH_PREFIX = '/v1/auth';
const TENANTS_PATH = '/v1/tenants';
let tenantSelectionValidated = false;

async function ensureTenantSelected(): Promise<void> {
  try {
    const response = await fetch(getApiUrl(TENANTS_PATH), {
      method: 'GET',
      credentials: 'include',
    });
    if (!response.ok) {
      return;
    }

    const data = (await response.json()) as TenantListResponse;
    const storedTenantId = getSelectedTenantId();
    const tenantIDs = new Set((data.items || []).map((tenant) => tenant.id));

    if (storedTenantId && tenantIDs.has(storedTenantId)) {
      tenantSelectionValidated = true;
      return;
    }

    const firstTenantId = data.items?.[0]?.id;
    if (firstTenantId) {
      setSelectedTenantId(firstTenantId);
      if (typeof window !== 'undefined') {
        window.dispatchEvent(new Event('tenant-changed'));
      }
    } else if (storedTenantId) {
      // Stored tenant became invalid (or tenants are empty): avoid sending stale X-Tenant-ID.
      clearSelectedTenantId();
    }
    tenantSelectionValidated = true;
  } catch {
    // Ignore tenant selection failures; caller will handle 401s.
  }
}

async function apiRequest<T>(
  method: string,
  path: string,
  body?: unknown
): Promise<T> {
  const apiKey = getApiKey();
  const headers: HeadersInit = {
    'Content-Type': 'application/json',
  };

  if (apiKey) {
    headers.Authorization = `Bearer ${apiKey}`;
  }

  const isAuthPath = path.startsWith(AUTH_PATH_PREFIX);
  const isTenantsPath = path.startsWith(TENANTS_PATH);

  if (!apiKey && !isAuthPath && !isTenantsPath) {
    // Ensure the selected tenant exists before attaching X-Tenant-ID.
    if (!tenantSelectionValidated || !getSelectedTenantId()) {
      await ensureTenantSelected();
    }
  }

  if (!apiKey && isAuthPath) {
    // Auth transitions can invalidate cached tenant context.
    tenantSelectionValidated = false;
  }

  if (!apiKey && isTenantsPath) {
    // Tenant list may have changed since last validation.
    tenantSelectionValidated = false;
  }

  const tenantId = getSelectedTenantId();
  if (tenantId) {
    headers['X-Tenant-ID'] = tenantId;
  }

  const options: RequestInit = {
    method,
    headers,
    credentials: 'include',
  };

  if (body) {
    options.body = JSON.stringify(body);
  }

  try {
    const response = await fetch(getApiUrl(path), options);
    if (response.status === 401 && !apiKey) {
      tenantSelectionValidated = false;
      const refreshed = await tryRefresh();
      if (refreshed) {
        const retryResponse = await fetch(getApiUrl(path), options);
        return handleResponse<T>(retryResponse, Boolean(apiKey));
      }
    }
    return handleResponse<T>(response, Boolean(apiKey));
  } catch (error) {
    if (error instanceof Error) {
      throw error;
    }
    throw new Error('Network error');
  }
}

async function tryRefresh(): Promise<boolean> {
  try {
    const response = await fetch(getApiUrl('/v1/auth/refresh'), {
      method: 'POST',
      credentials: 'include',
    });
    return response.ok;
  } catch {
    return false;
  }
}

// Monitor API functions
export async function getMonitors(params?: {
  tag?: string;
  enabled?: boolean;
  page?: number;
  page_size?: number;
}): Promise<MonitorListResponse> {
  const queryParams = new URLSearchParams();
  if (params?.tag) queryParams.append('tag', params.tag);
  if (params?.enabled !== undefined) queryParams.append('enabled', String(params.enabled));
  if (params?.page) queryParams.append('page', String(params.page));
  if (params?.page_size) queryParams.append('page_size', String(params.page_size));

  const queryString = queryParams.toString();
  const path = `/v1/monitors${queryString ? `?${queryString}` : ''}`;
  return apiRequest<MonitorListResponse>('GET', path);
}

export async function getMonitor(id: string): Promise<Monitor> {
  return apiRequest<Monitor>('GET', `/v1/monitors/${id}`);
}

export async function runMonitorNow(id: string): Promise<RunMonitorNowResponse> {
  return apiRequest<RunMonitorNowResponse>('POST', `/v1/monitors/${id}/run`);
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
}

export interface TestMonitorConfigResponse {
  status: 'success' | 'failure' | 'error';
  latency_ms?: number;
  error_message?: string;
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

// Dependency API functions
export async function getDependencyGraph(): Promise<DependencyGraph> {
  return apiRequest<DependencyGraph>('GET', '/v1/monitors/dependency-graph');
}

export async function getMonitorDependencies(id: string): Promise<{ items: DependencyMonitor[] }> {
  return apiRequest<{ items: DependencyMonitor[] }>('GET', `/v1/monitors/${id}/dependencies`);
}

export async function getMonitorDependents(id: string): Promise<{ items: DependencyMonitor[] }> {
  return apiRequest<{ items: DependencyMonitor[] }>('GET', `/v1/monitors/${id}/dependents`);
}

export async function addMonitorDependency(id: string, dependsOnId: string): Promise<void> {
  return apiRequest<void>('POST', `/v1/monitors/${id}/dependencies`, { depends_on_id: dependsOnId });
}

export async function removeMonitorDependency(id: string, dependsOnId: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/monitors/${id}/dependencies/${dependsOnId}`);
}

// Incident API functions
export async function getIncidents(params?: {
  page?: number;
  page_size?: number;
}): Promise<IncidentListResponse> {
  const queryParams = new URLSearchParams();
  if (params?.page) queryParams.append('page', String(params.page));
  if (params?.page_size) queryParams.append('page_size', String(params.page_size));

  const queryString = queryParams.toString();
  const path = `/v1/incidents${queryString ? `?${queryString}` : ''}`;
  return apiRequest<IncidentListResponse>('GET', path);
}

export async function getIncident(id: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('GET', `/v1/incidents/${id}`);
}

export async function createIncident(data: CreateIncidentRequest): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('POST', '/v1/incidents', data);
}

export async function updateIncident(id: string, data: UpdateIncidentRequest): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('PATCH', `/v1/incidents/${id}`, data);
}

export async function transitionIncidentState(id: string, state: IncidentState): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('POST', `/v1/incidents/${id}/state`, { state });
}

export async function addIncidentTimelineEntry(
  id: string,
  data: CreateIncidentTimelineEntryRequest
): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('POST', `/v1/incidents/${id}/timeline`, data);
}

export async function attachIncidentMonitor(id: string, monitorId: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('POST', `/v1/incidents/${id}/monitors`, { monitor_id: monitorId });
}

export async function detachIncidentMonitor(id: string, monitorId: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('DELETE', `/v1/incidents/${id}/monitors/${monitorId}`);
}

export async function attachIncidentAlert(id: string, alertId: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('POST', `/v1/incidents/${id}/alerts`, { alert_id: alertId });
}

export async function detachIncidentAlert(id: string, alertId: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('DELETE', `/v1/incidents/${id}/alerts/${alertId}`);
}

export async function publishIncidentToStatusPage(
  id: string,
  statusPageId: string,
  data: PublishIncidentToStatusPageRequest
): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('PUT', `/v1/incidents/${id}/status-pages/${statusPageId}`, data);
}

export async function unpublishIncidentFromStatusPage(id: string, statusPageId: string): Promise<IncidentDetail> {
  return apiRequest<IncidentDetail>('DELETE', `/v1/incidents/${id}/status-pages/${statusPageId}`);
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

// Alert API functions
export async function getAlerts(params?: {
  status?: string;
  monitor_id?: string;
  since?: string;
  page?: number;
  page_size?: number;
}): Promise<AlertListResponse> {
  const queryParams = new URLSearchParams();
  if (params?.status) queryParams.append('status', params.status);
  if (params?.monitor_id) queryParams.append('monitor_id', params.monitor_id);
  if (params?.since) queryParams.append('since', params.since);
  if (params?.page) queryParams.append('page', String(params.page));
  if (params?.page_size) queryParams.append('page_size', String(params.page_size));

  const queryString = queryParams.toString();
  const path = `/v1/alerts${queryString ? `?${queryString}` : ''}`;
  return apiRequest<AlertListResponse>('GET', path);
}

export async function getRecentAlerts(limit = 10): Promise<Alert[]> {
  const queryParams = new URLSearchParams();
  queryParams.append('limit', String(limit));
  return apiRequest<Alert[]>('GET', `/v1/alerts/recent?${queryParams.toString()}`);
}

export async function acknowledgeAlert(id: string): Promise<Alert> {
  return apiRequest<Alert>('POST', `/v1/alerts/${id}/acknowledge`);
}

export async function resolveAlert(id: string): Promise<Alert> {
  return apiRequest<Alert>('POST', `/v1/alerts/${id}/resolve`);
}

// Alert Channel API functions
export async function getAlertChannels(params?: {
  page?: number;
  page_size?: number;
}): Promise<AlertChannelListResponse> {
  const queryParams = new URLSearchParams();
  if (params?.page) queryParams.append('page', String(params.page));
  if (params?.page_size) queryParams.append('page_size', String(params.page_size));

  const queryString = queryParams.toString();
  const path = `/v1/alert-channels${queryString ? `?${queryString}` : ''}`;
  return apiRequest<AlertChannelListResponse>('GET', path);
}

export async function getAlertChannel(id: string): Promise<AlertChannel> {
  return apiRequest<AlertChannel>('GET', `/v1/alert-channels/${id}`);
}

export async function createAlertChannel(
  data: CreateAlertChannelRequest
): Promise<AlertChannel> {
  return apiRequest<AlertChannel>('POST', '/v1/alert-channels', data);
}

export async function updateAlertChannel(
  id: string,
  data: UpdateAlertChannelRequest
): Promise<AlertChannel> {
  return apiRequest<AlertChannel>('PATCH', `/v1/alert-channels/${id}`, data);
}

export async function deleteAlertChannel(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/alert-channels/${id}`);
}

export async function testAlertChannel(id: string): Promise<void> {
  return apiRequest<void>('POST', `/v1/alert-channels/${id}/test`);
}

export async function getAlertChannelPlugins(): Promise<PluginManifest[]> {
  return apiRequest<PluginManifest[]>('GET', '/v1/alert-channel-plugins');
}

export async function getAlertChannelPlugin(type: string): Promise<PluginManifest> {
  return apiRequest<PluginManifest>('GET', `/v1/alert-channel-plugins/${type}`);
}

// API Key functions
export async function getApiKeys(params?: {
  page?: number;
  page_size?: number;
}): Promise<ApiKeyListResponse> {
  const queryParams = new URLSearchParams();
  if (params?.page) queryParams.append('page', String(params.page));
  if (params?.page_size) queryParams.append('page_size', String(params.page_size));

  const queryString = queryParams.toString();
  const path = `/v1/api-keys${queryString ? `?${queryString}` : ''}`;
  return apiRequest<ApiKeyListResponse>('GET', path);
}

export async function createApiKey(
  data: CreateApiKeyRequest
): Promise<ApiKey> {
  return apiRequest<ApiKey>('POST', '/v1/api-keys', data);
}

export async function revokeApiKey(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/api-keys/${id}`);
}

// Status Page API functions
export async function getStatusPages(params?: {
  page?: number;
  page_size?: number;
}): Promise<StatusPageListResponse> {
  const queryParams = new URLSearchParams();
  if (params?.page) queryParams.append('page', String(params.page));
  if (params?.page_size) queryParams.append('page_size', String(params.page_size));

  const queryString = queryParams.toString();
  const path = `/v1/status-pages${queryString ? `?${queryString}` : ''}`;
  return apiRequest<StatusPageListResponse>('GET', path);
}

export async function getStatusPage(id: string): Promise<StatusPage> {
  return apiRequest<StatusPage>('GET', `/v1/status-pages/${id}`);
}

// Tenant API functions (admin only)
export interface GetUsersParams {
  page?: number;
  page_size?: number;
}

export async function getTenants(): Promise<TenantListResponse> {
  return apiRequest<TenantListResponse>('GET', '/v1/tenants');
}

export async function getTenantSettings(): Promise<TenantSettings> {
  return apiRequest<TenantSettings>('GET', '/v1/tenant-settings');
}

export async function updateTenantSettings(
  data: UpdateTenantSettingsRequest
): Promise<TenantSettings> {
  return apiRequest<TenantSettings>('PATCH', '/v1/tenant-settings', data);
}

// Admin users API functions (admin only)
export async function getUsers(params?: GetUsersParams): Promise<AdminUserListResponse> {
  const queryParams = new URLSearchParams();
  if (params?.page) queryParams.append('page', String(params.page));
  if (params?.page_size) queryParams.append('page_size', String(params.page_size));

  const queryString = queryParams.toString();
  const path = `/v1/users${queryString ? `?${queryString}` : ''}`;
  return apiRequest<AdminUserListResponse>('GET', path);
}

export async function createUser(data: CreateAdminUserRequest): Promise<AdminUser> {
  return apiRequest<AdminUser>('POST', '/v1/users', data);
}

export async function getUser(id: string): Promise<AdminUser> {
  return apiRequest<AdminUser>('GET', `/v1/users/${id}`);
}

export async function updateUser(id: string, data: UpdateAdminUserRequest): Promise<AdminUser> {
  return apiRequest<AdminUser>('PATCH', `/v1/users/${id}`, data);
}

export async function deleteUser(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/users/${id}`);
}

export async function createStatusPage(
  data: CreateStatusPageRequest
): Promise<StatusPage> {
  return apiRequest<StatusPage>('POST', '/v1/status-pages', data);
}

export async function updateStatusPage(
  id: string,
  data: UpdateStatusPageRequest
): Promise<StatusPage> {
  return apiRequest<StatusPage>('PATCH', `/v1/status-pages/${id}`, data);
}

export async function deleteStatusPage(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/status-pages/${id}`);
}

// Monitor Results API functions
export async function getMonitorResults(
  id: string,
  params?: { limit?: number; since?: string }
): Promise<MonitorResultsResponse> {
  const queryParams = new URLSearchParams();
  if (params?.limit) queryParams.append('limit', String(params.limit));
  if (params?.since) queryParams.append('since', params.since);

  const queryString = queryParams.toString();
  const path = `/v1/monitors/${id}/results${queryString ? `?${queryString}` : ''}`;
  return apiRequest<MonitorResultsResponse>('GET', path);
}

export async function getMonitorAnalytics(
  id: string,
  params?: { range?: '1h' | '6h' | '24h' | '7d' | '30d' | '90d' | '365d' }
): Promise<MonitorAnalyticsResponse> {
  const queryParams = new URLSearchParams();
  if (params?.range) queryParams.append('range', params.range);

  const queryString = queryParams.toString();
  const path = `/v1/monitors/${id}/analytics${queryString ? `?${queryString}` : ''}`;
  return apiRequest<MonitorAnalyticsResponse>('GET', path);
}

export async function getDashboardOverview(params?: {
  range?: '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
  failures_limit?: number;
  alerts_limit?: number;
  tags?: string[];
}): Promise<DashboardOverviewResponse> {
  const queryParams = new URLSearchParams();
  if (params?.range) queryParams.append('range', params.range);
  if (params?.failures_limit) queryParams.append('failures_limit', String(params.failures_limit));
  if (params?.alerts_limit) queryParams.append('alerts_limit', String(params.alerts_limit));
  params?.tags?.forEach((tag) => queryParams.append('tag', tag));

  const queryString = queryParams.toString();
  const path = `/v1/dashboard/overview${queryString ? `?${queryString}` : ''}`;
  return apiRequest<DashboardOverviewResponse>('GET', path);
}

export async function getDashboardSummary(params?: {
  range?: '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
  tags?: string[];
}): Promise<DashboardSummaryResponse> {
  const queryParams = new URLSearchParams();
  if (params?.range) queryParams.append('range', params.range);
  params?.tags?.forEach((tag) => queryParams.append('tag', tag));

  const queryString = queryParams.toString();
  const path = `/v1/dashboard/summary${queryString ? `?${queryString}` : ''}`;
  return apiRequest<DashboardSummaryResponse>('GET', path);
}

export async function getDashboardGroupSparkline(params: {
  group: string | null;
  range: '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
  tags?: string[];
}): Promise<DashboardGroupSparklineResponse> {
  const queryParams = new URLSearchParams();
  if (params.group) queryParams.append('group', params.group);
  queryParams.append('range', params.range);
  params.tags?.forEach((tag) => queryParams.append('tag', tag));

  return apiRequest<DashboardGroupSparklineResponse>(
    'GET',
    `/v1/dashboard/group-sparkline?${queryParams.toString()}`
  );
}

export async function getDashboardProblemMonitors(params?: {
  range?: '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
  limit?: number;
  tags?: string[];
}): Promise<DashboardProblemMonitorsResponse> {
  const queryParams = new URLSearchParams();
  if (params?.range) queryParams.append('range', params.range);
  if (params?.limit) queryParams.append('limit', String(params.limit));
  params?.tags?.forEach((tag) => queryParams.append('tag', tag));

  const queryString = queryParams.toString();
  const path = `/v1/dashboard/problem-monitors${queryString ? `?${queryString}` : ''}`;
  return apiRequest<DashboardProblemMonitorsResponse>('GET', path);
}

export async function getDashboardRecentFailures(params?: {
  range?: '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
  limit?: number;
  tags?: string[];
}): Promise<DashboardRecentFailuresResponse> {
  const queryParams = new URLSearchParams();
  if (params?.range) queryParams.append('range', params.range);
  if (params?.limit) queryParams.append('limit', String(params.limit));
  params?.tags?.forEach((tag) => queryParams.append('tag', tag));

  const queryString = queryParams.toString();
  const path = `/v1/dashboard/recent-failures${queryString ? `?${queryString}` : ''}`;
  return apiRequest<DashboardRecentFailuresResponse>('GET', path);
}

export async function getDashboardRecentAlerts(params?: {
  range?: '1h' | '24h' | '7d' | '30d' | '90d' | '365d';
  limit?: number;
  tags?: string[];
}): Promise<DashboardRecentAlertsResponse> {
  const queryParams = new URLSearchParams();
  if (params?.range) queryParams.append('range', params.range);
  if (params?.limit) queryParams.append('limit', String(params.limit));
  params?.tags?.forEach((tag) => queryParams.append('tag', tag));

  const queryString = queryParams.toString();
  const path = `/v1/dashboard/recent-alerts${queryString ? `?${queryString}` : ''}`;
  return apiRequest<DashboardRecentAlertsResponse>('GET', path);
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
  const queryParams = new URLSearchParams();
  if (resolvedBackendUrl) queryParams.append('backend_url', resolvedBackendUrl);

  const queryString = queryParams.toString();
  const path = `/v1/monitors/${monitorId}/agent/install${queryString ? `?${queryString}` : ''}`;
  return apiRequest<AgentInstallCommand>('GET', path);
}

// Push API functions
export async function getPushInfo(
  monitorId: string,
  backendUrl?: string
): Promise<PushInfo> {
  const resolvedBackendUrl =
    backendUrl || (typeof window !== 'undefined' ? window.location.origin : undefined);
  const queryParams = new URLSearchParams();
  if (resolvedBackendUrl) queryParams.append('backend_url', resolvedBackendUrl);

  const queryString = queryParams.toString();
  const path = `/v1/monitors/${monitorId}/push/info${queryString ? `?${queryString}` : ''}`;
  return apiRequest<PushInfo>('GET', path);
}

// Import API functions
export async function previewImport(file: File): Promise<ImportPreviewResponse> {
  const apiKey = getApiKey();
  if (!apiKey) {
    await ensureTenantSelected();
  }

  const formData = new FormData();
  formData.append('file', file);

  const makeRequest = async (): Promise<Response> => {
    const headers: HeadersInit = {};

    if (apiKey) {
      headers.Authorization = `Bearer ${apiKey}`;
    } else {
      const tenantId = getSelectedTenantId();
      if (tenantId) {
        headers['X-Tenant-ID'] = tenantId;
      }
    }

    return fetch(getApiUrl('/v1/monitors/import/preview'), {
      method: 'POST',
      headers,
      body: formData,
      credentials: 'include',
    });
  };

  let response = await makeRequest();
  if (response.status === 401 && !apiKey) {
    const refreshed = await tryRefresh();
    if (refreshed) {
      response = await makeRequest();
    }
  }

  if (response.status === 401) {
    if (apiKey) {
      clearApiKey();
      if (typeof window !== 'undefined') {
        window.location.href = '/connect';
      }
    } else if (typeof window !== 'undefined') {
      window.location.href = '/login';
    }
    throw new Error('Unauthorized');
  }

  // Try to parse as JSON, handle non-JSON responses gracefully
  let data;
  const contentType = response.headers.get('content-type');
  if (contentType && contentType.includes('application/json')) {
    try {
      data = await response.json();
    } catch {
      throw new Error('Invalid response from server');
    }
  } else {
    const text = await response.text();
    throw new Error(text || `Server error: ${response.status}`);
  }

  if (!response.ok) {
    const error: ApiError = data.error
      ? { error: data.error, message: data.message || data.error }
      : { error: 'unknown_error', message: data.message || 'An error occurred' };
    throw error;
  }

  return data;
}

export async function executeImport(data: ImportExecuteRequest): Promise<ImportExecuteResponse> {
  return apiRequest<ImportExecuteResponse>('POST', '/v1/monitors/import', data);
}

// Notification Settings API functions
export async function getNotificationSettings(): Promise<NotificationSettings> {
  return apiRequest<NotificationSettings>('GET', '/v1/notification-settings');
}

export async function updateNotificationSettings(
  data: Partial<NotificationSettings>
): Promise<NotificationSettings> {
  return apiRequest<NotificationSettings>('PUT', '/v1/notification-settings', data);
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
  const apiKey = getApiKey();
  if (!apiKey) {
    await ensureTenantSelected();
  }

  const makeRequest = async (): Promise<Response> => {
    const headers: HeadersInit = {};

    if (apiKey) {
      headers.Authorization = `Bearer ${apiKey}`;
    } else {
      const tenantId = getSelectedTenantId();
      if (tenantId) {
        headers['X-Tenant-ID'] = tenantId;
      }
    }

    return fetch(getApiUrl('/v1/monitors/export'), {
      method: 'GET',
      headers,
      credentials: 'include',
    });
  };

  let response = await makeRequest();
  if (response.status === 401 && !apiKey) {
    const refreshed = await tryRefresh();
    if (refreshed) {
      response = await makeRequest();
    }
  }

  if (response.status === 401) {
    if (apiKey) {
      clearApiKey();
      if (typeof window !== 'undefined') {
        window.location.href = '/connect';
      }
    } else if (typeof window !== 'undefined') {
      window.location.href = '/login';
    }
    throw new Error('Unauthorized');
  }

  if (!response.ok) {
    const contentType = response.headers.get('content-type');
    if (contentType && contentType.includes('application/json')) {
      const data = await response.json();
      const error: ApiError = data.error
        ? { error: data.error, message: data.message || data.error }
        : { error: 'unknown_error', message: data.message || 'An error occurred' };
      throw error;
    }

    throw new Error(`Export failed with status ${response.status}`);
  }

  const contentDisposition = response.headers.get('content-disposition') || '';
  const filenameMatch = contentDisposition.match(/filename=\"?([^\";]+)\"?/i);

  return {
    blob: await response.blob(),
    filename: filenameMatch?.[1] || 'monitors-export.yaml',
  };
}
