import type {
  DeleteWorkspaceResponse,
  Tenant,
  TenantListResponse,
  TenantSettings,
  UpdateTenantSettingsRequest,
  WorkspaceOverviewResponse,
} from '../types';
import { apiRequest } from './core';

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

// Workspace management (superadmins only).

export async function getWorkspaceOverview(): Promise<WorkspaceOverviewResponse> {
  return apiRequest<WorkspaceOverviewResponse>('GET', '/v1/tenants/overview');
}

export async function createWorkspace(name: string): Promise<Tenant> {
  return apiRequest<Tenant>('POST', '/v1/tenants', { name });
}

export async function renameWorkspace(id: string, name: string): Promise<Tenant> {
  return apiRequest<Tenant>('PATCH', `/v1/tenants/${id}`, { name });
}

/** Deletes a workspace for good; confirmName must repeat its name. */
export async function deleteWorkspace(id: string, confirmName: string): Promise<DeleteWorkspaceResponse> {
  return apiRequest<DeleteWorkspaceResponse>('DELETE', `/v1/tenants/${id}`, { confirm_name: confirmName });
}
