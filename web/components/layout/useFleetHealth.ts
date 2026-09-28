'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { getAlerts } from '@/lib/api';
import { TENANT_CHANGED_EVENT } from '@/lib/tenant';
import { useAlertEvents } from '@/components/alerts/AlertStreamProvider';

/**
 * Count of active (unacknowledged) alerts for the shell's fleet-health line,
 * mirrored onto html[data-health] so the ambient field blooms while anything
 * is open (see AmbientBackdrop). Null until the first load, or when the count
 * cannot be read.
 *
 * Call it once, in the shell: it owns the data-health attribute.
 */
export function useFleetHealth(): number | null {
  const [openAlerts, setOpenAlerts] = useState<number | null>(null);
  const { subscribe } = useAlertEvents();
  const reloadTimer = useRef<number | undefined>(undefined);

  const load = useCallback(async () => {
    try {
      const res = await getAlerts({ status: 'active', page_size: 1 });
      setOpenAlerts(res.total ?? 0);
    } catch {
      setOpenAlerts(null);
    }
  }, []);

  useEffect(() => {
    void load();
    const onTenantChange = () => void load();
    window.addEventListener(TENANT_CHANGED_EVENT, onTenantChange);
    return () => window.removeEventListener(TENANT_CHANGED_EVENT, onTenantChange);
  }, [load]);

  // Every stream event can move the count; debounce so an alert storm costs
  // one request rather than one per event.
  useEffect(
    () =>
      subscribe(() => {
        window.clearTimeout(reloadTimer.current);
        reloadTimer.current = window.setTimeout(() => void load(), 1500);
      }),
    [subscribe, load]
  );

  useEffect(() => {
    const root = document.documentElement;
    if (openAlerts && openAlerts > 0) root.dataset.health = 'alert';
    else delete root.dataset.health;
  }, [openAlerts]);

  useEffect(
    () => () => {
      window.clearTimeout(reloadTimer.current);
      delete document.documentElement.dataset.health;
    },
    []
  );

  return openAlerts;
}
