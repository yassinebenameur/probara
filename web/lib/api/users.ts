import type {
  AdminUser,
  AdminUserListResponse,
  AuthContext,
  CreateAdminUserRequest,
  CreateOidcGroupMappingRequest,
  OidcGroupMapping,
  OidcGroupMappingListResponse,
  OidcStatus,
  UpdateAdminUserRequest,
} from '../types';
import { apiRequest, getApiUrl, withQuery } from './core';

// Tenant API functions (admin only)
export interface GetUsersParams {
  page?: number;
  page_size?: number;
}

// Admin users API functions (admin only)
export async function getUsers(params?: GetUsersParams): Promise<AdminUserListResponse> {
  return apiRequest<AdminUserListResponse>(
    'GET',
    withQuery('/v1/users', {
      page: params?.page,
      page_size: params?.page_size,
    })
  );
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
