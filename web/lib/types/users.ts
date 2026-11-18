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
  /** Locks failed two-factor codes caused in the last 24 hours (/users reads only). */
  second_factor_lockouts?: number;
  last_second_factor_lockout_at?: string;
  /** Locked until a superadmin unlocks it or the user resets the password (/users reads only). */
  lockout_held?: boolean;
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
