'use client';

import { useEffect, useState } from 'react';
import { ChevronsUpDown } from 'lucide-react';
import { hasApiKey } from '@/lib/auth';
import {
  clearSelectedTenantId,
  emitTenantChange,
  getSelectedTenantId,
  setSelectedTenantId,
} from '@/lib/tenant';
import { getTenants } from '@/lib/api';
import type { Tenant } from '@/lib/types';
import { useSelectedTenantId } from '@/components/providers/TenantProvider';

/**
 * Tenant picker in the sidebar foot. Admin sessions load the tenant list,
 * settle the selection (stored id if still valid, else the first tenant) and
 * announce it; the picker only renders when there is more than one tenant.
 * Mounted once: the sidebar is a single instance on every breakpoint.
 */
export default function TenantSwitcher() {
  const [mounted, setMounted] = useState(false);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [isAdmin, setIsAdmin] = useState(false);
  // The provider is the single source of truth for the selection; this picker
  // only writes to it, so no local copy can drift out of sync.
  const selectedTenantId = useSelectedTenantId();

  useEffect(() => {
    setMounted(true);
  }, []);

  useEffect(() => {
    let isActive = true;

    if (!mounted) return;
    if (hasApiKey()) return; // API-key sessions are pinned to one tenant

    getTenants()
      .then((response) => {
        if (!isActive) return;
        setIsAdmin(true);
        setTenants(response.items || []);

        const storedTenantId = getSelectedTenantId();
        const fallbackTenantId = response.items?.[0]?.id;
        const nextTenantId =
          storedTenantId && response.items.some((tenant) => tenant.id === storedTenantId)
            ? storedTenantId
            : fallbackTenantId || null;

        if (nextTenantId) {
          setSelectedTenantId(nextTenantId);
        } else {
          clearSelectedTenantId();
        }
        emitTenantChange();
      })
      .catch(() => {
        if (isActive) {
          setIsAdmin(false);
        }
      });

    return () => {
      isActive = false;
    };
  }, [mounted]);

  if (!isAdmin || tenants.length <= 1) return null;

  return (
    <label className="block">
      <span className="mb-1 block px-1 text-2xs font-medium text-slate-500">Workspace</span>
      <span className="relative block">
      <select
        className="w-full cursor-pointer appearance-none truncate rounded-lg border border-edge-control bg-control py-2 pl-3 pr-8 text-ui font-medium text-slate-200 transition-colors hover:bg-control-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-cyan-500/40"
        value={selectedTenantId ?? ''}
        onChange={(e) => {
          setSelectedTenantId(e.target.value);
          emitTenantChange();
        }}
      >
        {tenants.map((tenant) => (
          <option key={tenant.id} value={tenant.id}>
            {tenant.name}
          </option>
        ))}
      </select>
      <ChevronsUpDown
        className="pointer-events-none absolute right-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-slate-500"
        strokeWidth={1.75}
        aria-hidden="true"
      />
      </span>
    </label>
  );
}
