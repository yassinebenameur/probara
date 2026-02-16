'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useState, useEffect, useCallback } from 'react';
import { getMonitors, getAlertChannels, getAlertPolicies, getStatusPages } from '@/lib/api';
import { clearApiKey, hasApiKey } from '@/lib/auth';
import { clearSelectedTenantId } from '@/lib/tenant';

const navItems = [
  { 
    name: 'Dashboard', 
    href: '/', 
    icon: (
      <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" />
      </svg>
    )
  },
  { 
    name: 'Monitors', 
    href: '/monitors',
    icon: (
      <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
      </svg>
    )
  },
  { 
    name: 'Status Pages', 
    href: '/status-pages',
    icon: (
      <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
      </svg>
    )
  },
  { 
    name: 'Alert Policies', 
    href: '/alert-policies',
    icon: (
      <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9" />
      </svg>
    )
  },
  {
    name: 'Alert Channels',
    href: '/alert-channels',
    icon: (
      <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M7 8h10M7 12h6m-6 4h10M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
      </svg>
    )
  },
  {
    name: 'Users',
    href: '/users',
    icon: (
      <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M17 20h5V18a4 4 0 00-5.356-3.771M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2V18a4 4 0 015.356-3.771M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0z" />
      </svg>
    )
  },
  { 
    name: 'Settings', 
    href: '/settings',
    icon: (
      <svg className="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z" />
        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
      </svg>
    )
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
    const handler = () => {
      loadCounts();
    };
    window.addEventListener('tenant-changed', handler);
    return () => {
      window.removeEventListener('tenant-changed', handler);
    };
  }, [loadCounts]);

  const getCount = (href: string) => {
    if (href === '/monitors') return counts.monitors;
    if (href === '/status-pages') return counts.statusPages;
    if (href === '/alert-policies') return counts.alertPolicies;
    if (href === '/alert-channels') return counts.alertChannels;
    return null;
  };

  const visibleNavItems = apiKeyMode ? navItems.filter((item) => item.href !== '/users') : navItems;

  return (
    <aside className="sticky top-0 flex h-screen w-64 flex-col border-r border-white/[0.06] bg-slate-950/50 backdrop-blur-xl">
      {/* Logo */}
      <div className="flex h-16 items-center gap-3 border-b border-white/[0.06] px-5">
        <div className="flex h-9 w-9 items-center justify-center rounded-xl bg-gradient-to-br from-cyan-400 to-violet-500">
          <svg className="h-5 w-5 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
          </svg>
        </div>
        <div>
          <h1 className="text-sm font-semibold text-white">Probara</h1>
          <p className="text-xs text-slate-500">Monitoring Platform</p>
        </div>
      </div>

      {/* Navigation */}
      <nav className="flex-1 space-y-1 p-3">
        {visibleNavItems.map((item) => {
          const isActive = pathname === item.href || (item.href !== '/' && pathname?.startsWith(item.href));
          const count = getCount(item.href);
          
          return (
            <Link
              key={item.name}
              href={item.href}
              className={`group flex items-center justify-between rounded-lg px-3 py-2.5 text-sm transition-all ${
                isActive
                  ? 'bg-white/[0.08] text-white'
                  : 'text-slate-400 hover:bg-white/[0.04] hover:text-white'
              }`}
            >
              <div className="flex items-center gap-3">
                <span className={`transition-colors ${isActive ? 'text-cyan-400' : 'text-slate-500 group-hover:text-slate-400'}`}>
                  {item.icon}
                </span>
                <span className="font-medium">{item.name}</span>
              </div>
              {count !== null && count > 0 && (
                <span className={`min-w-[20px] rounded-full px-2 py-0.5 text-center text-xs ${
                  isActive 
                    ? 'bg-cyan-500/20 text-cyan-400' 
                    : 'bg-slate-800 text-slate-500'
                }`}>
                  {count}
                </span>
              )}
            </Link>
          );
        })}
      </nav>

      {/* Footer */}
      <div className="border-t border-white/[0.06] p-4">
        <div className="flex items-center gap-3 rounded-lg bg-emerald-500/10 p-3">
          <div className="flex h-2.5 w-2.5 items-center justify-center">
            <span className="h-2.5 w-2.5 animate-pulse rounded-full bg-emerald-400 shadow-[0_0_8px_rgba(52,211,153,0.6)]" />
          </div>
          <div>
            <p className="text-xs font-medium text-emerald-400">All systems operational</p>
            <p className="text-xs text-slate-500">{counts.monitors} monitors active</p>
          </div>
        </div>
        
        {/* Logout Button */}
        <button
          onClick={handleLogout}
          className="btn btn-secondary w-full mt-3"
        >
          <svg className="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1" />
          </svg>
          Logout
        </button>
      </div>
    </aside>
  );
}
