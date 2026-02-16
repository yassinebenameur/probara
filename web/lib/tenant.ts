const TENANT_STORAGE_KEY = 'probara_selected_tenant_id';

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
