'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import { getAlerts } from '@/lib/api';
import { TENANT_CHANGED_EVENT } from '@/lib/tenant';
import { useAlertEvents } from '@/components/alerts/AlertStreamProvider';

/**
 * Count of active (unacknowledged) alerts for the sidebar's FleetHealth row
 * and the small-screen TopBar. Null until the first load, or when the count
 * cannot be read. Fleet health is shown there only; nothing tints the page.
 *
 * Call it once, in the shell, and pass the count down.
 */
export function useFleetHealth(): number | null {
  const [openAlerts, setOpenAlerts] = useState<number | null>(null);
  const { subscribe, subscribeResync } = useAlertEvents();
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

  // Events missed while the stream was down never arrive; re-read once on
  // reconnect so the badge cannot stay stale.
  useEffect(() => subscribeResync(() => void load()), [subscribeResync, load]);

  useEffect(() => () => window.clearTimeout(reloadTimer.current), []);

  return openAlerts;
}
