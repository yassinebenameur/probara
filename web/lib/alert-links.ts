// The /alerts deep link a notification carries: `?alert=<id>&tenant=<id>`.
// shared/notifications/present (suspectedCauseAction) builds it; the alerts
// page parses it here and nowhere else.

export interface AlertFocus {
  alertId: string;
  /** The alert's workspace, when the link names one. */
  tenantId: string | null;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** Reads the focus params; null unless `alert` is a UUID. A malformed `tenant` is ignored. */
export function parseAlertFocus(params: { get(name: string): string | null }): AlertFocus | null {
  const alertId = params.get('alert')?.trim() ?? '';
  if (!UUID.test(alertId)) return null;
  const tenant = params.get('tenant')?.trim() ?? '';
  return { alertId, tenantId: UUID.test(tenant) ? tenant : null };
}

/**
 * The workspace to switch to before loading a focused alert: the link's
 * tenant when it differs from the selected one and the viewer can see it.
 * Null means stay put (no tenant in the link, already selected, an API-key
 * session with no selection, or a workspace the viewer has no access to).
 */
export function focusTenantSwitch(
  focus: AlertFocus,
  selectedTenantId: string | null,
  visibleTenantIds: readonly string[]
): string | null {
  const target = focus.tenantId;
  if (!target || !selectedTenantId || target === selectedTenantId) return null;
  return visibleTenantIds.includes(target) ? target : null;
}

/** The params left after leaving the focused view. */
export function withoutAlertFocus(params: URLSearchParams): URLSearchParams {
  const next = new URLSearchParams(params.toString());
  next.delete('alert');
  next.delete('tenant');
  return next;
}
