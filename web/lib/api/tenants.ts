import type { TenantListResponse, TenantSettings, UpdateTenantSettingsRequest } from '../types';
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
