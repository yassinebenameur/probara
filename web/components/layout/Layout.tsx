'use client';

import { usePathname } from 'next/navigation';
import { AlertStreamProvider } from '@/components/alerts/AlertStreamProvider';
import Sidebar from './Sidebar';
import AuthGuard from './AuthGuard';

export default function Layout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();

  // Don't show layout on auth pages
  if (pathname === '/connect' || pathname === '/login') {
    return <>{children}</>;
  }

  return (
    <AuthGuard>
      <AlertStreamProvider>
        <div className="flex min-h-screen">
          <Sidebar />
          <main className="min-w-0 flex-1 overflow-auto">
            <div className="mx-auto max-w-7xl p-4 sm:p-6 lg:p-8">
              {children}
            </div>
          </main>
        </div>
      </AlertStreamProvider>
    </AuthGuard>
  );
}
