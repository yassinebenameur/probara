'use client';

import Panel from '@/components/ui/Panel';
import { formatDateTime } from '@/lib/format';
import type { AdminUser } from '@/lib/types';

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs text-slate-500">{label}</dt>
      <dd className="mt-0.5 text-ui text-slate-300">{children}</dd>
    </div>
  );
}

/** The signed-in user's account details (read-only; a superadmin edits users under Users). */
export default function ProfilePanel({ user }: { user: AdminUser }) {
  const superadmin = user.platform_role === 'superadmin';
  const memberships = user.memberships ?? [];
  return (
    <Panel title="Profile" subtitle="Your account. A superadmin changes names, email addresses and roles under Users.">
      <dl className="grid gap-4 sm:grid-cols-2">
        <Row label="Username">{user.username}</Row>
        <Row label="Email">{user.email || '—'}</Row>
        <Row label="Sign-in method">{user.auth_method === 'oidc' ? 'SSO (OIDC)' : 'Password'}</Row>
        <Row label="Platform role">{superadmin ? 'Superadmin (every workspace)' : 'Member'}</Row>
        {!superadmin && (
          <div className="sm:col-span-2">
            <dt className="text-xs text-slate-500">Workspaces</dt>
            <dd className="mt-0.5 text-ui text-slate-300">
              {memberships.length === 0
                ? 'None'
                : memberships.map((m) => `${m.tenant_name || m.tenant_id} (${m.role})`).join(', ')}
            </dd>
          </div>
        )}
        <Row label="Last sign-in">{formatDateTime(user.last_login_at, '—')}</Row>
        <Row label="Account created">{formatDateTime(user.created_at, '—')}</Row>
      </dl>
    </Panel>
  );
}
