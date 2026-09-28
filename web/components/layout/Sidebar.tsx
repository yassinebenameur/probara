'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useState, useEffect, useCallback } from 'react';
import {
  LayoutDashboard,
  Activity,
  FileText,
  AlertTriangle,
  Users,
  Settings,
  LogOut,
  Siren,
  Workflow,
  Wrench,
  MapPin,
  ScrollText,
  Target,
  X,
} from 'lucide-react';
import { getIncidents } from '@/lib/api';
import { clearApiKey, hasApiKey } from '@/lib/auth';
import { TENANT_CHANGED_EVENT, clearSelectedTenantId } from '@/lib/tenant';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import BrandMark from '@/components/ui/BrandMark';
import ThemeToggle from '@/components/ui/ThemeToggle';
import { ICON_BUTTON_CLASS } from '@/components/ui/IconButton';
import TenantSwitcher from './TenantSwitcher';
import FleetHealth from './FleetHealth';

type NavItem = {
  name: string;
  href: string;
  icon: React.ElementType;
  // Other routes that live under this entry (tabs folded into its page).
  also?: string[];
  showOpenIncidents?: boolean;
};

// Groups are separated by space alone; the names are only for keys.
const navGroups: { key: string; items: NavItem[] }[] = [
  {
    key: 'overview',
    items: [{ name: 'Dashboard', href: '/', icon: LayoutDashboard }],
  },
  {
    key: 'monitoring',
    items: [
      { name: 'Monitors', href: '/monitors', icon: Activity },
      { name: 'Locations', href: '/locations', icon: MapPin, also: ['/mesh'] },
      { name: 'Dependencies', href: '/dependencies', icon: Workflow },
      { name: 'Maintenance', href: '/maintenance', icon: Wrench },
      { name: 'Status pages', href: '/status-pages', icon: FileText },
      { name: 'SLAs', href: '/slas', icon: Target },
    ],
  },
  {
    key: 'alerting',
    items: [
      { name: 'Alerts', href: '/alerts', icon: AlertTriangle },
      { name: 'Incidents', href: '/incidents', icon: Siren, showOpenIncidents: true },
    ],
  },
  {
    key: 'admin',
    items: [
      { name: 'Users', href: '/users', icon: Users },
      { name: 'Audit log', href: '/audit', icon: ScrollText },
      { name: 'Settings', href: '/settings', icon: Settings, also: ['/alert-channels'] },
    ],
  },
];

interface SidebarProps {
  /** Drawer state below md; ignored from md up, where the sidebar is always shown. */
  open: boolean;
  onClose: () => void;
  openAlerts: number | null;
}

/**
 * The shell's one piece of chrome: a floating glass rail on desktop, a drawer
 * on small screens. It is a single instance on every breakpoint, so the
 * tenant bootstrap in TenantSwitcher and the counts here load once.
 */
export default function Sidebar({ open, onClose, openAlerts }: SidebarProps) {
  const pathname = usePathname();
  const router = useRouter();
  const [apiKeyMode, setApiKeyMode] = useState(true);
  const [openIncidents, setOpenIncidents] = useState(0);

  const handleLogout = () => {
    if (hasApiKey()) {
      clearApiKey();
      router.push('/connect');
      return;
    }

    fetch('/api/v1/auth/logout', {
      method: 'POST',
      credentials: 'include',
    }).finally(() => {
      clearSelectedTenantId();
      router.push('/login');
    });
  };

  const loadCounts = useCallback(async () => {
    try {
      const incidentsRes = await getIncidents({ page_size: 1 });
      setOpenIncidents(incidentsRes.open_total || 0);
    } catch (error) {
      console.error('Failed to load counts:', error);
    }
  }, []);

  useEffect(() => {
    loadCounts();
  }, [loadCounts]);

  useEffect(() => {
    setApiKeyMode(hasApiKey());
  }, []);

  useEffect(() => {
    const handler = () => loadCounts();
    window.addEventListener(TENANT_CHANGED_EVENT, handler);
    return () => window.removeEventListener(TENANT_CHANGED_EVENT, handler);
  }, [loadCounts]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [open, onClose]);

  const { isSuperadmin, role, loading: userLoading } = useCurrentUser();

  // /users is superadmin-only; /audit needs tenant admin (or superadmin).
  // While identity is loading, hide gated items rather than flashing them.
  const canSeeUsers = !apiKeyMode && !userLoading && isSuperadmin;
  const canSeeAudit = !apiKeyMode && !userLoading && (isSuperadmin || role === 'admin');

  const visibleGroups = navGroups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => {
        if (item.href === '/users') return canSeeUsers;
        if (item.href === '/audit') return canSeeAudit;
        return true;
      }),
    }))
    .filter((group) => group.items.length > 0);

  return (
    <>
      {open && (
        <div className="glass-scrim fixed inset-0 z-40 md:hidden" onClick={onClose} aria-hidden="true" />
      )}
      <aside
        className={`glass-shell fixed inset-y-3 left-3 z-50 flex w-64 flex-col transition-transform duration-200 ease-out md:sticky md:inset-auto md:top-3 md:z-auto md:my-3 md:ml-3 md:h-[calc(100vh-1.5rem)] md:w-60 md:flex-shrink-0 md:translate-x-0 ${
          open ? 'translate-x-0' : '-translate-x-[calc(100%+1rem)]'
        }`}
        aria-label="Main navigation"
      >
        <div className="flex items-start justify-between gap-2 px-4 pb-3 pt-4">
          <Link href="/" onClick={onClose} className="flex min-w-0 items-center gap-2.5 rounded-lg">
            <BrandMark size={28} className="flex-shrink-0" />
            <span className="truncate text-[15px] font-semibold tracking-tight text-white">Probara</span>
          </Link>
          <button type="button" onClick={onClose} aria-label="Close navigation" className={`${ICON_BUTTON_CLASS} md:hidden`}>
            <X strokeWidth={1.75} />
          </button>
        </div>

        <div className="px-3">
          <FleetHealth openAlerts={openAlerts} onNavigate={onClose} />
        </div>

        <nav className="dashboard-scroll mt-3 flex-1 space-y-4 overflow-y-auto px-3 pb-3">
          {visibleGroups.map((group) => (
            <div key={group.key} className="space-y-0.5">
              {group.items.map((item) => {
                const isActive =
                  pathname === item.href ||
                  [item.href, ...(item.also ?? [])].some(
                    (prefix) => prefix !== '/' && pathname?.startsWith(prefix)
                  );
                const count = item.showOpenIncidents ? openIncidents : 0;
                const Icon = item.icon;

                return (
                  <Link
                    key={item.name}
                    href={item.href}
                    onClick={onClose}
                    aria-current={isActive ? 'page' : undefined}
                    className={`group flex items-center justify-between gap-2 rounded-lg px-2.5 py-[7px] text-[13.5px] transition-colors ${
                      isActive
                        ? 'bg-control-hover font-medium text-white shadow-[inset_0_0_0_1px_var(--control-edge)]'
                        : 'text-slate-400 hover:bg-well-hover hover:text-slate-100'
                    }`}
                  >
                    <span className="flex min-w-0 items-center gap-2.5">
                      <Icon
                        className={`h-4 w-4 flex-shrink-0 transition-colors ${
                          isActive ? 'text-cyan-500' : 'text-slate-500 group-hover:text-slate-300'
                        }`}
                        strokeWidth={1.75}
                      />
                      <span className="truncate">{item.name}</span>
                    </span>
                    {count > 0 && (
                      <span className="rounded-full bg-rose-500/15 px-1.5 text-[11px] font-semibold tabular-nums leading-5 text-rose-300">
                        {count}
                      </span>
                    )}
                  </Link>
                );
              })}
            </div>
          ))}
        </nav>

        <div className="space-y-2 border-t border-divider p-3">
          <TenantSwitcher />
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={handleLogout}
              className="flex flex-1 items-center gap-2.5 rounded-lg px-2.5 py-[7px] text-[13.5px] text-slate-400 transition-colors hover:bg-well-hover hover:text-slate-100"
            >
              <LogOut className="h-4 w-4 text-slate-500" strokeWidth={1.75} aria-hidden="true" />
              Sign out
            </button>
            <ThemeToggle />
          </div>
        </div>
      </aside>
    </>
  );
}
