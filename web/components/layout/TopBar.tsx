'use client';

import { useState, useEffect } from 'react';
import { hasApiKey } from '@/lib/auth';
import { clearSelectedTenantId, getSelectedTenantId, setSelectedTenantId } from '@/lib/tenant';
import { getTenants } from '@/lib/api';
import type { Tenant } from '@/lib/types';
import ThemeToggle from '@/components/ui/ThemeToggle';

/**
 * Slim workspace bar above the page content: tenant switcher (admin
 * sessions with more than one tenant) and the theme toggle.
 */
export default function TopBar() {
  const [mounted, setMounted] = useState(false);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [selectedTenantId, setSelectedTenantIdState] = useState<string | null>(null);
  const [isAdmin, setIsAdmin] = useState(false);

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
          setSelectedTenantIdState(nextTenantId);
          notifyTenantChange();
        } else {
          clearSelectedTenantId();
          setSelectedTenantIdState(null);
        }
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

  const notifyTenantChange = () => {
    if (typeof window !== 'undefined') {
      window.dispatchEvent(new Event('tenant-changed'));
    }
  };

  const handleTenantChange = (value: string) => {
    setSelectedTenantId(value);
    setSelectedTenantIdState(value);
    notifyTenantChange();
  };

  return (
    <header className="sticky top-0 z-30 flex h-12 items-center justify-end gap-3 border-b border-white/[0.06] bg-slate-950/70 px-4 backdrop-blur-xl sm:px-6 lg:px-8">
      {isAdmin && tenants.length > 1 && (
        <label className="flex items-center gap-2 rounded-full border border-white/[0.08] bg-slate-900/85 px-3 py-1 text-xs">
          <span className="text-[0.7rem] uppercase tracking-wide text-slate-500">Tenant</span>
          <select
            className="bg-transparent text-xs text-slate-200 outline-none"
            value={selectedTenantId ?? ''}
            onChange={(e) => handleTenantChange(e.target.value)}
          >
            {tenants.map((tenant) => (
              <option key={tenant.id} value={tenant.id} className="bg-slate-900 text-slate-200">
                {tenant.name}
              </option>
            ))}
          </select>
        </label>
      )}
      <ThemeToggle />
    </header>
  );
}
