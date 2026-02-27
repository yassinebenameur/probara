'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useState, useEffect, useCallback } from 'react';
import {
  LayoutDashboard,
  Activity,
  FileText,
  AlertTriangle,
  Bell,
  Send,
  Users,
  Settings,
  LogOut,
  Zap,
  CheckCircle2,
} from 'lucide-react';
import { getMonitors, getAlertChannels, getAlertPolicies, getStatusPages } from '@/lib/api';
import { clearApiKey, hasApiKey } from '@/lib/auth';
import { clearSelectedTenantId } from '@/lib/tenant';

type NavItem = {
  name: string;
  href: string;
  icon: React.ElementType;
  countKey?: 'monitors' | 'statusPages' | 'alertPolicies' | 'alertChannels';
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
      { name: 'Status Pages', href: '/status-pages', icon: FileText, countKey: 'statusPages' },
    ],
  },
  {
    label: 'Alerting',
    items: [
      { name: 'Alerts', href: '/alerts', icon: AlertTriangle },
      { name: 'Alert Policies', href: '/alert-policies', icon: Bell, countKey: 'alertPolicies' },
      { name: 'Alert Channels', href: '/alert-channels', icon: Send, countKey: 'alertChannels' },
    ],
  },
  {
    label: 'Administration',
    items: [
      { name: 'Users', href: '/users', icon: Users },
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
    alertPolicies: 0,
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
      const [monitorsRes, pagesRes, policiesRes, channelsRes] = await Promise.all([
        getMonitors({ page_size: 1 }),
        getStatusPages({ page_size: 1 }),
        getAlertPolicies({ page_size: 1 }),
        getAlertChannels({ page_size: 1 }),
      ]);
      setCounts({
        monitors: monitorsRes.total || 0,
        statusPages: pagesRes.total || 0,
        alertPolicies: policiesRes.total || 0,
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
    window.addEventListener('tenant-changed', handler);
    return () => window.removeEventListener('tenant-changed', handler);
  }, [loadCounts]);

  const getCount = (countKey?: NavItem['countKey']): number | null => {
    if (!countKey) return null;
    return counts[countKey] ?? null;
  };

  const visibleGroups = navGroups
    .map((group) => ({
      ...group,
      items: apiKeyMode ? group.items.filter((item) => item.href !== '/users') : group.items,
    }))
    .filter((group) => group.items.length > 0);

  return (
    <aside className="sticky top-0 flex h-screen w-60 flex-col border-r border-white/[0.06] bg-slate-950/50 backdrop-blur-xl">
      {/* Logo */}
      <div className="flex h-14 items-center gap-3 border-b border-white/[0.06] px-4">
        <div className="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg bg-gradient-to-br from-cyan-400 to-violet-500">
          <Zap className="h-4 w-4 text-white" strokeWidth={2.5} />
        </div>
        <div className="min-w-0">
          <h1 className="truncate text-sm font-semibold text-white">Probara</h1>
          <p className="truncate text-[0.7rem] text-slate-500">Monitoring Platform</p>
        </div>
      </div>

      {/* Navigation */}
      <nav className="flex-1 overflow-y-auto py-3">
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
                      <span
                        className={`min-w-[18px] rounded-full px-1.5 py-0.5 text-center text-[0.65rem] tabular-nums ${
                          isActive
                            ? 'bg-cyan-500/20 text-cyan-400'
                            : 'bg-slate-800 text-slate-500'
                        }`}
                      >
                        {count}
                      </span>
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
        {/* System status */}
        <div className="mb-3 flex items-center gap-2">
          <CheckCircle2 className="h-3.5 w-3.5 flex-shrink-0 text-emerald-400" strokeWidth={2} />
          <div className="min-w-0">
            <p className="truncate text-[0.72rem] font-medium text-emerald-400">All systems operational</p>
            <p className="text-[0.68rem] text-slate-600">{counts.monitors} monitor{counts.monitors !== 1 ? 's' : ''} active</p>
          </div>
        </div>

        {/* Logout */}
        <button
          onClick={handleLogout}
          className="flex w-full items-center gap-2 rounded-md px-2.5 py-2 text-sm text-slate-500 transition-all hover:bg-white/[0.04] hover:text-slate-300"
        >
          <LogOut className="h-4 w-4 flex-shrink-0" strokeWidth={1.75} />
          <span className="font-medium">Logout</span>
        </button>
      </div>
    </aside>
  );
}
