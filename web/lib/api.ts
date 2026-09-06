import { getApiKey, clearApiKey } from './auth';
import {
  clearSelectedTenantId,
  emitTenantChange,
  getSelectedTenantId,
  setSelectedTenantId,
} from './tenant';
import type {
  Monitor,
  CreateMonitorRequest,
  UpdateMonitorRequest,
  MonitorListResponse,
  DBMetricsEnvelope,
  PrometheusMetrics,
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
  StatusPageTemplateState,
  StatusPageLibraryTemplate,
  StatusPageLibraryTemplateListResponse,
  MonitorResultsResponse,
  MonitorAnalyticsResponse,
  DependencyGraph,
  DependencyMonitor,
  DependencySuggestionResult,
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
  AuthContext,
  OidcStatus,
  OidcGroupMapping,
  OidcGroupMappingListResponse,
  CreateOidcGroupMappingRequest,
  AuditListResponse,
  IncidentListResponse,
  AISettings,
  AISettingsUpdate,
  AITestRequest,
  AITestResult,
  IncidentAIAnalysis,
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
  MaintenanceWindow,
  MaintenanceWindowListResponse,
  MaintenanceWindowStatus,
  CreateMaintenanceWindowRequest,
  UpdateMaintenanceWindowRequest,
  SnoozeMonitorRequest,
  Location,
  LocationListResponse,
  CreateLocationRequest,
  UpdateLocationRequest,
  LocationDeployInfo,
  MeshResponse,
  MeshEdgeHistoryPoint,
  MeshProbeResponse,
  MetricSeriesListResponse,
  MetricQueryRequest,
  MetricQueryResponse,
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
      emitTenantChange();
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
      const refreshed = await refreshSession();
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

// Refresh rotates the token server-side, so concurrent calls would race and
// all but one lose. Single-flight: every 401 handler (including the SSE
// stream) awaits the same in-flight request.
let refreshInFlight: Promise<boolean> | null = null;

export function refreshSession(): Promise<boolean> {
  if (!refreshInFlight) {
    refreshInFlight = doRefresh().finally(() => {
      refreshInFlight = null;
    });
  }
  return refreshInFlight;
}

async function doRefresh(): Promise<boolean> {
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
  metrics_data?: DBMetricsEnvelope & { prometheus?: PrometheusMetrics };
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

// Maintenance window API functions
export async function getMaintenanceWindows(params?: {
  status?: MaintenanceWindowStatus;
  monitor_id?: string;
  page?: number;
  page_size?: number;
}): Promise<MaintenanceWindowListResponse> {
  const queryParams = new URLSearchParams();
  if (params?.status) queryParams.append('status', params.status);
  if (params?.monitor_id) queryParams.append('monitor_id', params.monitor_id);
  if (params?.page) queryParams.append('page', String(params.page));
  if (params?.page_size) queryParams.append('page_size', String(params.page_size));

  const queryString = queryParams.toString();
  const path = `/v1/maintenance-windows${queryString ? `?${queryString}` : ''}`;
  return apiRequest<MaintenanceWindowListResponse>('GET', path);
}

export async function createMaintenanceWindow(
  data: CreateMaintenanceWindowRequest
): Promise<MaintenanceWindow> {
  return apiRequest<MaintenanceWindow>('POST', '/v1/maintenance-windows', data);
}

export async function updateMaintenanceWindow(
  id: string,
  data: UpdateMaintenanceWindowRequest
): Promise<MaintenanceWindow> {
  return apiRequest<MaintenanceWindow>('PATCH', `/v1/maintenance-windows/${id}`, data);
}

export async function deleteMaintenanceWindow(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/maintenance-windows/${id}`);
}

// Creates a single-monitor maintenance window from now until the given time.
export async function snoozeMonitor(
  monitorId: string,
  data: SnoozeMonitorRequest
): Promise<MaintenanceWindow> {
  return apiRequest<MaintenanceWindow>('POST', `/v1/monitors/${monitorId}/snooze`, data);
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

export async function suggestDependencies(): Promise<DependencySuggestionResult> {
  return apiRequest<DependencySuggestionResult>('POST', '/v1/monitors/dependency-suggestions');
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

// requestIncidentAIAnalysis enqueues an AI root cause analysis and returns the
// pending record. Throws on 503 when the LLM provider is not configured.
export async function requestIncidentAIAnalysis(id: string): Promise<IncidentAIAnalysis> {
  return apiRequest<IncidentAIAnalysis>('POST', `/v1/incidents/${id}/ai-analysis`);
}

// getIncidentAIAnalysis returns the latest analysis, or null when none exists.
export async function getIncidentAIAnalysis(id: string): Promise<IncidentAIAnalysis | null> {
  return apiRequest<IncidentAIAnalysis | null>('GET', `/v1/incidents/${id}/ai-analysis`);
}

// --- AI settings (per-tenant LLM config) ---

export async function getAISettings(): Promise<AISettings> {
  return apiRequest<AISettings>('GET', '/v1/ai-settings');
}

export async function updateAISettings(data: AISettingsUpdate): Promise<AISettings> {
  return apiRequest<AISettings>('PUT', '/v1/ai-settings', data);
}

export async function testAISettings(data: AITestRequest): Promise<AITestResult> {
  return apiRequest<AITestResult>('POST', '/v1/ai-settings/test', data);
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

// Auth context / SSO / audit log
export async function getAuthContext(): Promise<AuthContext> {
  return apiRequest<AuthContext>('GET', '/v1/auth-context');
}

// Unauthenticated: used by the login page before any session exists.
export async function getOidcStatus(): Promise<OidcStatus> {
  const response = await fetch(getApiUrl('/v1/auth/oidc/status'), { method: 'GET' });
  if (!response.ok) {
    return { enabled: false };
  }
  return (await response.json()) as OidcStatus;
}

// OIDC group→role mappings (superadmin only)
export async function listOidcGroupMappings(): Promise<OidcGroupMappingListResponse> {
  return apiRequest<OidcGroupMappingListResponse>('GET', '/v1/oidc-group-mappings');
}

export async function createOidcGroupMapping(
  data: CreateOidcGroupMappingRequest
): Promise<OidcGroupMapping> {
  return apiRequest<OidcGroupMapping>('POST', '/v1/oidc-group-mappings', data);
}

export async function updateOidcGroupMapping(
  id: string,
  data: { role?: string; label?: string }
): Promise<OidcGroupMapping> {
  return apiRequest<OidcGroupMapping>('PATCH', `/v1/oidc-group-mappings/${id}`, data);
}

export async function deleteOidcGroupMapping(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/oidc-group-mappings/${id}`);
}

export interface GetAuditLogParams {
  action?: string;
  outcome?: string;
  actor_id?: string;
  from?: string;
  to?: string;
  page?: number;
  page_size?: number;
}

export async function getAuditLog(params?: GetAuditLogParams): Promise<AuditListResponse> {
  const queryParams = new URLSearchParams();
  if (params?.action) queryParams.append('action', params.action);
  if (params?.outcome) queryParams.append('outcome', params.outcome);
  if (params?.actor_id) queryParams.append('actor_id', params.actor_id);
  if (params?.from) queryParams.append('from', params.from);
  if (params?.to) queryParams.append('to', params.to);
  if (params?.page) queryParams.append('page', String(params.page));
  if (params?.page_size) queryParams.append('page_size', String(params.page_size));

  const queryString = queryParams.toString();
  const path = `/v1/audit-log${queryString ? `?${queryString}` : ''}`;
  return apiRequest<AuditListResponse>('GET', path);
}

export async function getAuditActions(): Promise<{ actions: string[] }> {
  return apiRequest<{ actions: string[] }>('GET', '/v1/audit-log/actions');
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

// Status page custom template API functions

export async function getStatusPageTemplate(id: string): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('GET', `/v1/status-pages/${id}/template`);
}

export async function saveStatusPageTemplateDraft(
  id: string,
  source: string
): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('PUT', `/v1/status-pages/${id}/template/draft`, {
    source,
  });
}

export async function discardStatusPageTemplateDraft(
  id: string
): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('DELETE', `/v1/status-pages/${id}/template/draft`);
}

export async function publishStatusPageTemplate(id: string): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('POST', `/v1/status-pages/${id}/template/publish`);
}

export async function revertStatusPageTemplate(
  id: string,
  version: number
): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('POST', `/v1/status-pages/${id}/template/revert`, {
    version,
  });
}

export async function resetStatusPageTemplate(id: string): Promise<StatusPageTemplateState> {
  return apiRequest<StatusPageTemplateState>('DELETE', `/v1/status-pages/${id}/template`);
}

// Plain-text downloads (the template sources are not JSON).
async function apiRequestText(path: string): Promise<string> {
  const apiKey = getApiKey();
  const headers: HeadersInit = {};
  if (apiKey) {
    headers.Authorization = `Bearer ${apiKey}`;
  }
  const tenantId = getSelectedTenantId();
  if (tenantId) {
    headers['X-Tenant-ID'] = tenantId;
  }
  const response = await fetch(getApiUrl(path), {
    method: 'GET',
    headers,
    credentials: 'include',
  });
  if (!response.ok) {
    throw new Error(`HTTP error! status: ${response.status}`);
  }
  return response.text();
}

export async function getStatusPageDefaultTemplateSource(id: string): Promise<string> {
  return apiRequestText(`/v1/status-pages/${id}/template/default`);
}

export async function getStatusPageTemplateVersionSource(
  id: string,
  version: number
): Promise<string> {
  return apiRequestText(`/v1/status-pages/${id}/template/versions/${version}/source`);
}

// Status page template library (tenant-level reusable templates)

export async function listLibraryTemplates(): Promise<StatusPageLibraryTemplateListResponse> {
  return apiRequest<StatusPageLibraryTemplateListResponse>('GET', '/v1/status-page-templates');
}

export async function getLibraryTemplate(id: string): Promise<StatusPageLibraryTemplate> {
  return apiRequest<StatusPageLibraryTemplate>('GET', `/v1/status-page-templates/${id}`);
}

export async function createLibraryTemplate(data: {
  name: string;
  description?: string;
  source: string;
}): Promise<StatusPageLibraryTemplate> {
  return apiRequest<StatusPageLibraryTemplate>('POST', '/v1/status-page-templates', data);
}

export async function updateLibraryTemplate(
  id: string,
  data: { name?: string; description?: string; source?: string }
): Promise<StatusPageLibraryTemplate> {
  return apiRequest<StatusPageLibraryTemplate>('PATCH', `/v1/status-page-templates/${id}`, data);
}

export async function deleteLibraryTemplate(id: string): Promise<void> {
  return apiRequest<void>('DELETE', `/v1/status-page-templates/${id}`);
}

export async function getLibraryTemplateSource(id: string): Promise<string> {
  return apiRequestText(`/v1/status-page-templates/${id}/source`);
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

// Private location API functions
export async function getLocations(params?: {
  page?: number;
  page_size?: number;
}): Promise<LocationListResponse> {
  const queryParams = new URLSearchParams();
  if (params?.page) queryParams.append('page', String(params.page));
  if (params?.page_size) queryParams.append('page_size', String(params.page_size));

  const queryString = queryParams.toString();
  const path = `/v1/locations${queryString ? `?${queryString}` : ''}`;
  return apiRequest<LocationListResponse>('GET', path);
}

export async function createLocation(data: CreateLocationRequest): Promise<Location> {
  return apiRequest<Location>('POST', '/v1/locations', data);
}

export async function updateLocation(
  id: string,
  data: UpdateLocationRequest
): Promise<Location> {
  return apiRequest<Location>('PATCH', `/v1/locations/${id}`, data);
}

// Detaches the location from its monitors (their quorum is clamped server-side).
export async function deleteLocation(id: string): Promise<{ monitors_detached: number }> {
  return apiRequest<{ monitors_detached: number }>('DELETE', `/v1/locations/${id}`);
}

export async function getLocationDeployInfo(id: string): Promise<LocationDeployInfo> {
  return apiRequest<LocationDeployInfo>('GET', `/v1/locations/${id}/deploy`);
}

// Inter-location connectivity mesh API functions

export async function getMesh(): Promise<MeshResponse> {
  return apiRequest<MeshResponse>('GET', '/v1/mesh');
}

export async function getMeshEdgeHistory(
  sourceId: string,
  targetId: string,
  hours = 24
): Promise<{ points: MeshEdgeHistoryPoint[] }> {
  const params = new URLSearchParams({ source: sourceId, target: targetId, hours: String(hours) });
  return apiRequest<{ points: MeshEdgeHistoryPoint[] }>('GET', `/v1/mesh/history?${params}`);
}

// Ephemeral probe of one directed edge via the source location's worker.
export async function probeMeshEdge(
  sourceId: string,
  targetId: string
): Promise<MeshProbeResponse> {
  return apiRequest<MeshProbeResponse>('POST', '/v1/mesh/probe', {
    source_location_id: sourceId,
    target_location_id: targetId,
  });
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
    const refreshed = await refreshSession();
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
    const refreshed = await refreshSession();
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
