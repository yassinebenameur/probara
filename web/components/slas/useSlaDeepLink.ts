'use client';

import { useEffect } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { getTenants } from '@/lib/api';
import { focusTenantSwitch } from '@/lib/alert-links';
import { parseSlaLink, slaIssuedReportsHref } from '@/lib/sla-links';
import { emitTenantChange, setSelectedTenantId } from '@/lib/tenant';
import { useSelectedTenantId } from '@/components/providers/TenantProvider';

/**
 * Follows an emailed report's link (`/slas?sla=&tenant=`, lib/sla-links.ts)
 * as /alerts follows an alert link: switch to the link's workspace when the
 * viewer can see it (TenantScope then remounts the list under it and this
 * runs again), then open the SLA's issued reports. Returns whether a link is
 * being followed, so the list can hold back.
 */
export function useSlaDeepLink(): boolean {
  const params = useSearchParams();
  const router = useRouter();
  const selectedTenantId = useSelectedTenantId();
  const link = parseSlaLink(params);
  const slaId = link?.slaId ?? null;
  const tenantId = link?.tenantId ?? null;

  useEffect(() => {
    if (!slaId) return;
    let cancelled = false;
    (async () => {
      if (tenantId && selectedTenantId && tenantId !== selectedTenantId) {
        const tenants = await getTenants().catch(() => null);
        // The viewer may have navigated away meanwhile.
        if (cancelled) return;
        const target = focusTenantSwitch({ tenantId }, selectedTenantId, (tenants?.items ?? []).map((t) => t.id));
        if (target) {
          setSelectedTenantId(target);
          emitTenantChange();
          return;
        }
      }
      // Not visible, or already there: the SLA page says if it is missing.
      router.replace(slaIssuedReportsHref(slaId));
    })();
    return () => {
      cancelled = true;
    };
  }, [slaId, tenantId, selectedTenantId, router]);

  return slaId !== null;
}
