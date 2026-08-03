'use client';

import { Fragment, createContext, useContext, useEffect, useRef, useState } from 'react';
import { useParams, usePathname, useRouter } from 'next/navigation';
import { TENANT_CHANGED_EVENT, getSelectedTenantId } from '@/lib/tenant';

const TenantContext = createContext<string | null>(null);

/**
 * The tenant every request is currently scoped to, or null for API-key sessions
 * and before the first selection resolves.
 */
export function useSelectedTenantId(): string | null {
  return useContext(TenantContext);
}

export function TenantProvider({ children }: { children: React.ReactNode }) {
  // Seeded from storage so the event TopBar emits after its first tenant fetch
  // confirms the same value instead of looking like a switch.
  const [tenantId, setTenantId] = useState<string | null>(() => getSelectedTenantId());

  useEffect(() => {
    const sync = () => setTenantId(getSelectedTenantId());
    window.addEventListener(TENANT_CHANGED_EVENT, sync);
    return () => window.removeEventListener(TENANT_CHANGED_EVENT, sync);
  }, []);

  return <TenantContext.Provider value={tenantId}>{children}</TenantContext.Provider>;
}

/**
 * The list route a resource detail path belongs to, or null when the path isn't
 * a tenant-scoped detail route. `/monitors/<id>` and
 * `/status-pages/<id>/template` both resolve to their list page.
 */
function listRouteFor(pathname: string, idParam: string | null): string | null {
  if (!idParam) return null;

  const segments = pathname.split('/').filter(Boolean);
  const idIndex = segments.indexOf(idParam);
  // Users are platform-scoped, so /users/[id] stays valid across a switch.
  if (idIndex < 1 || segments[0] === 'users') return null;

  return `/${segments.slice(0, idIndex).join('/')}`;
}

/**
 * Remounts the page subtree when the tenant changes. Pages fetch on mount with
 * empty deps, so keying them on the tenant is what makes a switch reload the
 * new tenant's data — no per-page listener needed.
 *
 * Resource detail routes (`…/[id]`) fall back to their list page instead: the id
 * belongs to the tenant we just left, so refetching it would only 404. The
 * outgoing page is held back from rendering until that redirect lands, so it
 * never issues those doomed requests.
 */
export function TenantScope({ children }: { children: React.ReactNode }) {
  const tenantId = useSelectedTenantId();
  const pathname = usePathname();
  const params = useParams();
  const router = useRouter();
  const renderedTenantId = useRef(tenantId);

  const idParam = typeof params?.id === 'string' ? params.id : null;
  // A first resolution of the selection is not a switch.
  const switched = Boolean(
    renderedTenantId.current && tenantId && renderedTenantId.current !== tenantId
  );
  const redirectTarget = switched ? listRouteFor(pathname, idParam) : null;

  useEffect(() => {
    if (redirectTarget) {
      // Keep the ref on the old tenant so children stay suspended until the
      // navigation lands and clears redirectTarget.
      router.replace(redirectTarget);
      return;
    }
    renderedTenantId.current = tenantId;
  }, [redirectTarget, router, tenantId]);

  if (redirectTarget) return null;

  return <Fragment key={tenantId ?? 'no-tenant'}>{children}</Fragment>;
}
