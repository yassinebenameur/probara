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

// Workspace management (superadmins only).
export interface WorkspaceOverview {
  id: string;
  name: string;
  member_count: number;
  monitor_count: number;
  // The default workspace, and the one new SSO users join, cannot be deleted.
  protected: boolean;
  created_at: string;
  updated_at: string;
}

export interface WorkspaceOverviewResponse {
  items: WorkspaceOverview[];
}

export interface DeleteWorkspaceResponse {
  id: string;
  name: string;
  deleted_at: string;
}
