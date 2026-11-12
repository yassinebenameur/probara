import type { TenantRole } from './rbac';

// Tenant types
export interface Tenant {
  id: string;
  name: string;
  // Caller's membership role in this tenant ("admin" for superadmins);
  // present on list responses.
  role?: TenantRole;
  created_at: string;
  updated_at: string;
}

export interface TenantListResponse {
  items: Tenant[];
}

export interface TenantSettings {
  data_retention_days: number;
  dashboard_group_tags: string[];
}

export interface UpdateTenantSettingsRequest {
  data_retention_days?: number;
  dashboard_group_tags?: string[];
}
