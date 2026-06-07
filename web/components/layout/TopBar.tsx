'use client';

import { useState, useEffect } from 'react';
import { Search } from 'lucide-react';
import { hasApiKey, clearApiKey } from '@/lib/auth';
import { clearSelectedTenantId, getSelectedTenantId, setSelectedTenantId } from '@/lib/tenant';
import { getTenants } from '@/lib/api';
import type { Tenant } from '@/lib/types';
import { useRouter, usePathname } from 'next/navigation';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';

export default function TopBar() {
  const router = useRouter();
  const pathname = usePathname();
  const [connected, setConnected] = useState(false);
  const [mounted, setMounted] = useState(false);
  const [tenants, setTenants] = useState<Tenant[]>([]);
  const [selectedTenantId, setSelectedTenantIdState] = useState<string | null>(null);
  const [isAdmin, setIsAdmin] = useState(false);

  useEffect(() => {
    setMounted(true);
    setConnected(hasApiKey());
  }, []);

  useEffect(() => {
    let isActive = true;

    if (!mounted) return;
    if (hasApiKey()) return;

    getTenants()
      .then((response) => {
        if (!isActive) return;
        setIsAdmin(true);
        setConnected(true);
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

  const handleDisconnect = () => {
    clearApiKey();
    setConnected(false);
    router.push('/connect');
  };

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

  // Get page title based on pathname
  const getPageTitle = () => {
    if (pathname === '/') return 'Probara dashboard';
    if (pathname?.startsWith('/monitors')) return 'Monitors';
    if (pathname?.startsWith('/alerts')) return 'Alerts';
    if (pathname?.startsWith('/alert-channels')) return 'Alert Channels';
    if (pathname?.startsWith('/status-pages')) return 'Status Pages';
    if (pathname?.startsWith('/users')) return 'Users';
    if (pathname?.startsWith('/settings')) return 'Settings';
    return 'Dashboard';
  };

  const getPageSubtitle = () => {
    if (pathname === '/') return 'Monitor HTTP, TCP, WebSocket & custom checks across all regions.';
    if (pathname?.startsWith('/monitors')) return 'View and manage your uptime monitors';
    if (pathname?.startsWith('/alerts')) return 'View and triage active and historical alerts';
    if (pathname?.startsWith('/alert-channels')) return 'Manage delivery channels for alerts';
    if (pathname?.startsWith('/status-pages')) return 'Manage your public status pages';
    if (pathname?.startsWith('/users')) return 'Manage platform admin accounts';
    return '';
  };

  return (
    <header className="flex items-center justify-between gap-4 py-4">
      {/* Left side */}
      <div className="flex flex-col gap-1">
        <div className="flex items-center gap-2 text-xl font-semibold">
          {getPageTitle()}
          {pathname === '/' && (
            <Pill tone="success" size="xs" dot>
              Production
            </Pill>
          )}
        </div>
        {getPageSubtitle() && (
          <div className="text-[0.85rem] text-muted">{getPageSubtitle()}</div>
        )}
      </div>

      {/* Right side */}
      <div className="flex items-center gap-3">
        {isAdmin && tenants.length > 0 && (
          <div className="flex items-center gap-2 rounded-full border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.85)] px-3 py-1 text-xs text-muted">
            <span className="text-[0.75rem] uppercase tracking-wide text-slate-400">Tenant</span>
            <select
              className="bg-transparent text-xs text-gray-200 outline-none"
              value={selectedTenantId ?? ''}
              onChange={(e) => handleTenantChange(e.target.value)}
            >
              {tenants.map((tenant) => (
                <option key={tenant.id} value={tenant.id} className="bg-slate-900">
                  {tenant.name}
                </option>
              ))}
            </select>
          </div>
        )}

        {/* Environment Switch */}
        <div className="flex items-center gap-2 rounded-full border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.85)] px-2.5 py-1.5 text-xs text-muted">
          <span className="h-2 w-2 rounded-full bg-success shadow-[0_0_0_4px_rgba(34,197,94,0.28)]" />
          <span>Environment</span>
          <span className="rounded-full border border-[rgba(148,163,184,0.4)] bg-[rgba(15,23,42,0.9)] px-1.5 py-0.5 text-[0.78rem] text-gray-200">
            prod
          </span>
          <span className="rounded-full border border-[rgba(148,163,184,0.4)] bg-[rgba(15,23,42,0.9)] px-1.5 py-0.5 text-[0.78rem] text-gray-200 opacity-60">
            staging
          </span>
        </div>

        {/* Search */}
        <div className="relative min-w-[220px]">
          <span className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-slate-500">
            <Search className="h-3.5 w-3.5" strokeWidth={1.75} />
          </span>
          <input
            type="text"
            placeholder="Search checks, incidents, status pages..."
            className="input input-sm w-full rounded-full pl-8 pr-14 text-xs"
          />
          <span className="pointer-events-none absolute right-2 top-1/2 flex -translate-y-1/2 items-center gap-0.5 rounded-md border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.9)] px-1.5 py-0.5 text-[0.7rem] text-[rgba(156,163,175,0.3)]">
            <span>Ctrl</span>
            <span>K</span>
          </span>
        </div>

        {/* User Pill */}
        <div className="flex cursor-pointer items-center gap-2 rounded-full border border-[rgba(255,255,255,0.06)] bg-[rgba(15,23,42,0.92)] px-2.5 py-1 text-xs">
          <div className="flex h-[22px] w-[22px] items-center justify-center rounded-full bg-[linear-gradient(135deg,#4f46e5,#22c55e)] text-xs">
            Y
          </div>
          <span className="text-gray-200">Yassine</span>
          <div className={`h-[7px] w-[7px] rounded-full ${mounted && connected ? 'bg-success' : 'bg-danger'}`} />
        </div>

        {mounted && connected && (
          <Button variant="ghost" size="xs" onClick={handleDisconnect}>
            Disconnect
          </Button>
        )}
      </div>
    </header>
  );
}
