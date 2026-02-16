'use client';

import { useEffect, useState } from 'react';
import { usePathname, useRouter } from 'next/navigation';
import { hasApiKey } from '@/lib/auth';

export default function AuthGuard({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const [checked, setChecked] = useState(false);
  const [authed, setAuthed] = useState(false);

  useEffect(() => {
    let isMounted = true;

    // Don't protect auth pages
    if (pathname === '/connect' || pathname === '/login') {
      setChecked(true);
      setAuthed(true);
      return;
    }

    // API key mode
    if (hasApiKey()) {
      setChecked(true);
      setAuthed(true);
      return;
    }

    // Admin session mode
    fetch('/api/v1/auth/me', { credentials: 'include' })
      .then((response) => {
        if (!isMounted) return;
        if (response.ok) {
          setAuthed(true);
        } else {
          router.push('/login');
        }
      })
      .catch(() => {
        if (isMounted) {
          router.push('/login');
        }
      })
      .finally(() => {
        if (isMounted) {
          setChecked(true);
        }
      });

    return () => {
      isMounted = false;
    };
  }, [pathname, router]);

  if (!checked) {
    return (
      <div className="flex h-64 items-center justify-center text-sm text-slate-500">
        Checking session...
      </div>
    );
  }

  if (!authed) {
    return (
      <div className="flex h-64 items-center justify-center text-sm text-slate-500">
        Redirecting to login...
      </div>
    );
  }

  return <>{children}</>;
}
