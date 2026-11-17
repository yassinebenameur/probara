const TENANT_STORAGE_KEY = 'probara_selected_tenant_id';

/**
 * Fired after the selected tenant is written to storage. Everything that holds
 * tenant-scoped data listens for it; see TenantProvider for the page subtree.
 */
export const TENANT_CHANGED_EVENT = 'tenant-changed';

export function emitTenantChange(): void {
  if (typeof window === 'undefined') {
    return;
  }
  window.dispatchEvent(new Event(TENANT_CHANGED_EVENT));
}

/**
 * Fired when the list of workspaces may have changed: one was created,
 * renamed or deleted, or the selected one turned out to be deleted. The
 * tenant switcher (outside the page subtree) reloads its list and settles
 * the selection again; pages never listen for it.
 */
export const TENANTS_CHANGED_EVENT = 'tenants-changed';

export function emitTenantsChanged(): void {
  if (typeof window === 'undefined') {
    return;
  }
  window.dispatchEvent(new Event(TENANTS_CHANGED_EVENT));
}

/** The API's error code for a request scoped to a deleted workspace. */
export const WORKSPACE_NOT_FOUND = 'workspace_not_found';

/** Makes tenantId the selected workspace and announces the switch. */
export function selectTenant(tenantId: string): void {
  setSelectedTenantId(tenantId);
  emitTenantChange();
}

export function getSelectedTenantId(): string | null {
  if (typeof window === 'undefined') {
    return null;
  }
  return localStorage.getItem(TENANT_STORAGE_KEY);
}

export function setSelectedTenantId(tenantId: string): void {
  if (typeof window === 'undefined') {
    return;
  }
  localStorage.setItem(TENANT_STORAGE_KEY, tenantId);
}

export function clearSelectedTenantId(): void {
  if (typeof window === 'undefined') {
    return;
  }
  localStorage.removeItem(TENANT_STORAGE_KEY);
}
