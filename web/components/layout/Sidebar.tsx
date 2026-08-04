'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useState, useEffect, useCallback } from 'react';
import {
  LayoutDashboard,
  Activity,
  FileText,
  AlertTriangle,
  Send,
  Users,
  Settings,
  LogOut,
  Siren,
  Workflow,
  Wrench,
  MapPin,
  Network,
  ScrollText,
} from 'lucide-react';
import { getMonitors, getAlertChannels, getStatusPages, getIncidents } from '@/lib/api';
import { clearApiKey, hasApiKey } from '@/lib/auth';
import { TENANT_CHANGED_EVENT, clearSelectedTenantId } from '@/lib/tenant';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import Pill from '@/components/ui/Pill';
import Button from '@/components/ui/Button';
import BrandMark from '@/components/ui/BrandMark';

type NavItem = {
  name: string;
  href: string;
  icon: React.ElementType;
  countKey?: 'monitors' | 'statusPages' | 'alertChannels' | 'incidents';
};

type NavGroup = {
  label: string;
  items: NavItem[];
};

const navGroups: NavGroup[] = [
  {
    label: 'Overview',
    items: [
      { name: 'Dashboard', href: '/', icon: LayoutDashboard },
    ],
  },
  {
    label: 'Monitoring',
    items: [
      { name: 'Monitors', href: '/monitors', icon: Activity, countKey: 'monitors' },
      { name: 'Locations', href: '/locations', icon: MapPin },
      { name: 'Mesh', href: '/mesh', icon: Network },
      { name: 'Dependencies', href: '/dependencies', icon: Workflow },
      { name: 'Maintenance', href: '/maintenance', icon: Wrench },
      { name: 'Status Pages', href: '/status-pages', icon: FileText, countKey: 'statusPages' },
    ],
  },
  {
    label: 'Alerting',
    items: [
      { name: 'Alerts', href: '/alerts', icon: AlertTriangle },
      { name: 'Incidents', href: '/incidents', icon: Siren, countKey: 'incidents' },
      { name: 'Alert Channels', href: '/alert-channels', icon: Send, countKey: 'alertChannels' },
    ],
  },
  {
    label: 'Administration',
    items: [
      { name: 'Users', href: '/users', icon: Users },
      { name: 'Audit Log', href: '/audit', icon: ScrollText },
      { name: 'Settings', href: '/settings', icon: Settings },
    ],
  },
];

export default function Sidebar() {
  const pathname = usePathname();
  const router = useRouter();
  const [apiKeyMode, setApiKeyMode] = useState(true);
  const [counts, setCounts] = useState({
    monitors: 0,
    statusPages: 0,
    incidents: 0,
    alertChannels: 0,
  });

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
      const [monitorsRes, pagesRes, incidentsRes, channelsRes] = await Promise.all([
        getMonitors({ page_size: 1 }),
        getStatusPages({ page_size: 1 }),
        getIncidents({ page_size: 1 }),
        getAlertChannels({ page_size: 1 }),
      ]);
      setCounts({
        monitors: monitorsRes.total || 0,
        statusPages: pagesRes.total || 0,
        incidents: incidentsRes.total || 0,
        alertChannels: channelsRes.total || 0,
      });
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

  const getCount = (countKey?: NavItem['countKey']): number | null => {
    if (!countKey) return null;
    return counts[countKey] ?? null;
  };

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
    <aside className="sticky top-0 hidden h-screen w-60 flex-col border-r border-white/[0.06] bg-slate-950/50 backdrop-blur-xl md:flex">
      {/* Logo */}
      <div className="flex h-14 items-center gap-3 border-b border-white/[0.06] px-4">
        <BrandMark size={32} className="flex-shrink-0" />
        <div className="min-w-0">
          <h1 className="truncate text-sm font-semibold text-white">Probara</h1>
          <p className="truncate text-[0.7rem] text-slate-500">Monitoring Platform</p>
        </div>
      </div>

      {/* Navigation */}
      <nav className="dashboard-scroll flex-1 overflow-y-auto py-3">
        {visibleGroups.map((group, groupIdx) => (
          <div key={group.label} className={groupIdx > 0 ? 'mt-1' : ''}>
            <p className="mb-1 px-4 pt-3 text-[0.65rem] font-semibold uppercase tracking-widest text-slate-600">
              {group.label}
            </p>
            <div className="space-y-0.5 px-2">
              {group.items.map((item) => {
                const isActive =
                  pathname === item.href ||
                  (item.href !== '/' && pathname?.startsWith(item.href));
                const count = getCount(item.countKey);
                const Icon = item.icon;

                return (
                  <Link
                    key={item.name}
                    href={item.href}
                    className={`group flex items-center justify-between rounded-md px-2.5 py-2 text-sm transition-all ${
                      isActive
                        ? 'bg-white/[0.08] text-white'
                        : 'text-slate-400 hover:bg-white/[0.04] hover:text-slate-200'
                    }`}
                  >
                    <div className="flex items-center gap-2.5">
                      <Icon
                        className={`h-4 w-4 flex-shrink-0 transition-colors ${
                          isActive ? 'text-cyan-400' : 'text-slate-500 group-hover:text-slate-400'
                        }`}
                        strokeWidth={1.75}
                      />
                      <span className="font-medium">{item.name}</span>
                    </div>
                    {count !== null && count > 0 && (
                      <Pill tone={isActive ? 'info' : 'neutral'} size="xs" className="tabular-nums">
                        {count}
                      </Pill>
                    )}
                  </Link>
                );
              })}
            </div>
          </div>
        ))}
      </nav>

      {/* Footer */}
      <div className="border-t border-white/[0.06] px-4 py-3">
        <div className="mb-3 flex items-center gap-2">
          <Activity className="h-3.5 w-3.5 flex-shrink-0 text-cyan-400" strokeWidth={2} />
          <div className="min-w-0">
            <p className="truncate text-[0.72rem] font-medium text-slate-300">Monitoring workspace</p>
            <p className="text-[0.68rem] text-slate-600">{counts.monitors} monitor{counts.monitors !== 1 ? 's' : ''} active</p>
          </div>
        </div>

        <Button
          variant="subtle"
          size="sm"
          icon={<LogOut strokeWidth={1.75} />}
          onClick={handleLogout}
          className="w-full justify-start"
        >
          Logout
        </Button>
      </div>
    </aside>
  );
}
