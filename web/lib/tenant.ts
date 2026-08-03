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
