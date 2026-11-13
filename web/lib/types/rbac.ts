// RBAC types
export type TenantRole = "admin" | "editor" | "viewer";

export type PlatformRole = "superadmin" | "member";

export type ApiKeyScope = "read" | "write";

export interface TenantMembership {
  tenant_id: string;
  tenant_name?: string;
  role: TenantRole;
}

// Effective identity of the current credential (cookie or API key),
// from GET /v1/auth-context.
export interface AuthContext {
  actor_type: "admin_user" | "api_key" | "";
  admin_id?: string;
  api_key_id?: string;
  tenant_id?: string;
  platform_role?: PlatformRole;
  role?: TenantRole;
  scope?: ApiKeyScope;
  can_write: boolean;
}

export interface OidcStatus {
  enabled: boolean;
  label?: string;
}

// OIDC group→role mapping. No tenant_id = platform mapping (superadmin).
// Any existing mappings make the IdP the source of truth: SSO users' roles
// are re-derived from their groups on every login.
export interface OidcGroupMapping {
  id: string;
  group_name: string;
  // Operator-facing display name; matching always uses group_name.
  label?: string;
  tenant_id?: string;
  tenant_name?: string;
  role: PlatformRole | TenantRole;
  created_at: string;
  updated_at: string;
}

// IdP group observed in a verified ID token at a past SSO login — the
// mapping editor's suggestion source (OIDC has no group-enumeration API).
export interface OidcSeenGroup {
  group_name: string;
  first_seen_at: string;
  last_seen_at: string;
}

export interface OidcGroupMappingListResponse {
  mappings: OidcGroupMapping[];
  seen_groups: OidcSeenGroup[];
  groups_claim: string;
  groups_scope_requested: boolean;
}

export interface CreateOidcGroupMappingRequest {
  group_name: string;
  label?: string;
  tenant_id?: string;
  role: string;
}
