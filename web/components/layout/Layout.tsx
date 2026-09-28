'use client';

import { useCallback, useState } from 'react';
import { usePathname } from 'next/navigation';
import { AlertStreamProvider } from '@/components/alerts/AlertStreamProvider';
import { CurrentUserProvider } from '@/components/providers/CurrentUserProvider';
import { TenantProvider, TenantScope } from '@/components/providers/TenantProvider';
import Sidebar from './Sidebar';
import TopBar from './TopBar';
import AuthGuard from './AuthGuard';
import { useFleetHealth } from './useFleetHealth';

export default function Layout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();

  // Don't show layout on auth pages
  if (pathname === '/connect' || pathname === '/login') {
    return <>{children}</>;
  }

  return (
    <AuthGuard>
      <TenantProvider>
      <CurrentUserProvider>
      <AlertStreamProvider>
        <Shell>{children}</Shell>
      </AlertStreamProvider>
      </CurrentUserProvider>
      </TenantProvider>
    </AuthGuard>
  );
}

// Inside the providers: the fleet-health count needs the alert stream.
function Shell({ children }: { children: React.ReactNode }) {
  const [navOpen, setNavOpen] = useState(false);
  const closeNav = useCallback(() => setNavOpen(false), []);
  const openAlerts = useFleetHealth();

  return (
    <div className="flex min-h-screen">
      <Sidebar open={navOpen} onClose={closeNav} openAlerts={openAlerts} />
      {/* overflow-x-clip contains wide content without creating a scroll container,
    which would break position:sticky descendants (the window is the scroller) */}
      <main className="min-w-0 flex-1 overflow-x-clip">
        <TopBar onOpenNav={() => setNavOpen(true)} openAlerts={openAlerts} />
        <div className="mx-auto max-w-7xl px-4 pb-12 pt-5 sm:px-6 md:pt-8 lg:px-10">
          <TenantScope>{children}</TenantScope>
        </div>
      </main>
    </div>
  );
}
