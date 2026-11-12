import type { PlatformRole, TenantMembership, TenantRole } from './rbac';

// Admin user types
export interface AdminUser {
  id: string;
  username: string;
  email?: string;
  platform_role?: PlatformRole;
  auth_method?: "password" | "oidc";
  memberships?: TenantMembership[];
  created_at: string;
  updated_at: string;
  last_login_at?: string;
  disabled_at?: string;
}

export interface AdminUserListResponse {
  items: AdminUser[];
  page: number;
  page_size: number;
  total: number;
}

export interface MembershipInput {
  tenant_id: string;
  role: TenantRole;
}

export interface CreateAdminUserRequest {
  username: string;
  email?: string;
  // Omitted = SSO-only user (requires email for IdP linking).
  password?: string;
  platform_role?: PlatformRole;
  memberships?: MembershipInput[];
}

export interface UpdateAdminUserRequest {
  username?: string;
  email?: string;
  password?: string;
  platform_role?: PlatformRole;
  memberships?: MembershipInput[];
}
