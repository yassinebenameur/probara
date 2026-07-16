'use client';

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
} from 'react';
import { getAuthContext } from '@/lib/api';
import { hasApiKey } from '@/lib/auth';
import type { AdminUser, AuthContext, TenantRole } from '@/lib/types';

interface CurrentUserValue {
  /** Full user object; only available in admin-cookie mode. */
  user: AdminUser | null;
  actorType: 'admin_user' | 'api_key' | 'unknown';
  /** Effective role in the selected tenant ("admin" for superadmins). */
  role: TenantRole | null;
  isSuperadmin: boolean;
  /** Whether the current credential may perform mutations. */
  canWrite: boolean;
  loading: boolean;
  refresh: () => void;
}

// Default is permissive: on older APIs without /v1/auth-context the UI keeps
// its historical behavior (show everything; the server still enforces).
const permissiveDefault: CurrentUserValue = {
  user: null,
  actorType: 'unknown',
  role: null,
  isSuperadmin: true,
  canWrite: true,
  loading: false,
  refresh: () => {},
};

const CurrentUserContext = createContext<CurrentUserValue>(permissiveDefault);

export function useCurrentUser(): CurrentUserValue {
  return useContext(CurrentUserContext);
}

export function CurrentUserProvider({ children }: { children: React.ReactNode }) {
  const [value, setValue] = useState<CurrentUserValue>({ ...permissiveDefault, loading: true });
  const [reloadKey, setReloadKey] = useState(0);

  const refresh = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      let authContext: AuthContext | null = null;
      try {
        authContext = await getAuthContext();
      } catch {
        // Older API or transient failure: keep the permissive default.
      }

      let user: AdminUser | null = null;
      if (!hasApiKey()) {
        try {
          const response = await fetch('/api/v1/auth/me', { credentials: 'include' });
          if (response.ok) {
            const data = await response.json();
            user = (data?.user as AdminUser) ?? null;
          }
        } catch {
          // AuthGuard handles redirecting unauthenticated sessions.
        }
      }

      if (cancelled) return;
      if (!authContext) {
        setValue({ ...permissiveDefault, user, loading: false, refresh });
        return;
      }
      setValue({
        user,
        actorType: authContext.actor_type === 'api_key' ? 'api_key' : authContext.actor_type === 'admin_user' ? 'admin_user' : 'unknown',
        role: authContext.role ?? null,
        isSuperadmin: authContext.platform_role === 'superadmin',
        canWrite: authContext.can_write,
        loading: false,
        refresh,
      });
    }

    load();
    return () => {
      cancelled = true;
    };
  }, [reloadKey, refresh]);

  // Role can differ per tenant: refetch when the tenant switcher fires.
  useEffect(() => {
    const onTenantChanged = () => refresh();
    window.addEventListener('tenant-changed', onTenantChanged);
    return () => window.removeEventListener('tenant-changed', onTenantChanged);
  }, [refresh]);

  return <CurrentUserContext.Provider value={value}>{children}</CurrentUserContext.Provider>;
}
