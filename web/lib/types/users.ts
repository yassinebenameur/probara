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
  /** Set while password sign-in is locked after repeated failures (/users reads only). */
  locked_until?: string;
  /** Current run of failed sign-ins (/users reads only). */
  failed_login_count?: number;
  /** Whether the account has a password (/users reads only). */
  has_password?: boolean;
  /** Whether two-factor sign-in is on (/users reads only). */
  two_factor_enabled?: boolean;
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
