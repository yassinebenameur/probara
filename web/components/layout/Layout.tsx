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
  if (pathname === '/connect' || pathname === '/login' || pathname === '/oauth/consent') {
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

  // #app-root lets a modal mark the rest of the app inert. While the
  // small-screen drawer is open the page behind it is inert too, so focus and
  // AT stay in the drawer (the sidebar closes it when the viewport widens).
  return (
    <div id="app-root" className="flex min-h-screen">
      <a
        href="#main"
        className="glass-overlay sr-only !rounded-lg px-3 py-2 text-ui font-medium text-white focus:not-sr-only focus:fixed focus:left-3 focus:top-3 focus:z-[70] focus:outline-none focus-visible:ring-2 focus-visible:ring-focus"
      >
        Skip to content
      </a>
      <Sidebar open={navOpen} onClose={closeNav} openAlerts={openAlerts} />
      <div className="flex min-w-0 flex-1 flex-col" inert={navOpen || undefined}>
        <TopBar onOpenNav={() => setNavOpen(true)} openAlerts={openAlerts} />
        {/* overflow-x-clip contains wide content without creating a scroll
            container, which would break position:sticky descendants (the
            window is the scroller). The shell only sets gutters and a 2560px
            safety cap for ultrawides; each page picks its own width
            (components/ui/Page.tsx). */}
        <main id="main" tabIndex={-1} className="min-w-0 flex-1 overflow-x-clip focus:outline-none">
          <div className="mx-auto w-full max-w-[160rem] px-4 pb-12 pt-5 sm:px-6 md:pt-8 lg:px-8 2xl:px-10">
            <TenantScope>{children}</TenantScope>
          </div>
        </main>
      </div>
    </div>
  );
}
