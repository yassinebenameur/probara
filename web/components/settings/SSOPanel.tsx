'use client';

import { useEffect, useState } from 'react';
import Panel from '@/components/ui/Panel';
import Pill from '@/components/ui/Pill';
import { getOidcStatus } from '@/lib/api';
import type { OidcStatus } from '@/lib/types';

// Read-only status: OIDC is configured via environment/helm (OIDC_* vars),
// not editable at runtime.
export function SSOPanel() {
  const [status, setStatus] = useState<OidcStatus | null>(null);

  useEffect(() => {
    let ignore = false;
    getOidcStatus().then((s) => {
      if (!ignore) setStatus(s);
    });
    return () => {
      ignore = true;
    };
  }, []);

  return (
    <Panel
      title="Single sign-on"
      subtitle="OIDC SSO is configured through the OIDC_* environment variables (or helm auth.oidc values)."
    >
      {status === null ? (
        <div className="text-sm text-slate-500">Checking SSO status…</div>
      ) : (
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
          <div>
            <p className="text-sm font-medium text-white">
              {status.enabled ? `Login with “${status.label || 'SSO'}” is active` : 'SSO is not configured'}
            </p>
            <p className="mt-1 text-xs text-slate-400">
              {status.enabled
                ? 'Users can sign in through the identity provider from the login page.'
                : 'Set OIDC_ENABLED=true with issuer, client ID and secret to offer SSO login.'}
            </p>
          </div>
          <Pill tone={status.enabled ? 'success' : 'neutral'} size="xs" dot>
            {status.enabled ? 'Enabled' : 'Disabled'}
          </Pill>
        </div>
      )}
    </Panel>
  );
}
