'use client';

import { useCallback, useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { LogOut, MonitorSmartphone } from 'lucide-react';
import Page from '@/components/ui/Page';
import PageHeader from '@/components/ui/PageHeader';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { getMySessions, revokeAllMySessions, revokeMyOtherSessions, revokeMySession } from '@/lib/api';
import { hasApiKey } from '@/lib/auth';
import { clearSelectedTenantId } from '@/lib/tenant';
import { formatDateTime, formatRelativeTime, pluralize } from '@/lib/format';
import { describeUserAgent, sessionMethodLabel } from '@/lib/sessions';
import type { AdminSession } from '@/lib/types';
import ContactsPanel from '@/components/account/ContactsPanel';

type Pending =
  | { kind: 'one'; session: AdminSession }
  | { kind: 'others' }
  | { kind: 'all' };

/**
 * The signed-in user's own account: profile and the browsers signed in as
 * them. Every role can end its own sessions; ending one stops its access at
 * once (the server checks the session on every request).
 */
export default function AccountPage() {
  const router = useRouter();
  const { showToast } = useToast();
  const { user, isSuperadmin, loading: userLoading } = useCurrentUser();
  const [apiKeyMode, setApiKeyMode] = useState(false);
  const [sessions, setSessions] = useState<AdminSession[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [pending, setPending] = useState<Pending | null>(null);
  const [working, setWorking] = useState(false);

  const load = useCallback(async () => {
    try {
      setLoading(true);
      setError('');
      const res = await getMySessions();
      setSessions(res.items || []);
    } catch (err: any) {
      setError(err.message || 'Failed to load sessions');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const keyMode = hasApiKey();
    setApiKeyMode(keyMode);
    if (keyMode) {
      setLoading(false);
      return;
    }
    load();
  }, [load]);

  const signedOut = () => {
    clearSelectedTenantId();
    router.push('/login');
  };

  const confirm = async () => {
    if (!pending) return;
    setWorking(true);
    try {
      if (pending.kind === 'one') {
        await revokeMySession(pending.session.id);
        if (pending.session.current) {
          signedOut();
          return;
        }
        showToast('Session signed out', 'success');
      } else if (pending.kind === 'others') {
        const res = await revokeMyOtherSessions();
        showToast(`Signed out ${pluralize(res.revoked, 'other session')}`, 'success');
      } else {
        await revokeAllMySessions();
        signedOut();
        return;
      }
      setPending(null);
      await load();
    } catch (err: any) {
      showToast(err.message || 'Failed to sign out', 'error');
    } finally {
      setWorking(false);
    }
  };

  const others = sessions.filter((s) => !s.current).length;

  if (apiKeyMode) {
    return (
      <Page width="form">
        <PageHeader title="Account" />
        <Callout tone="info">
          You are connected with an API key. Account settings belong to a signed-in user; sign in
          with a username or SSO to manage your sessions.
        </Callout>
      </Page>
    );
  }

  return (
    <Page width="form">
      <PageHeader title="Account" subtitle="Your profile, the contacts you can be reached at and the browsers signed in as you." />

      <Panel title="Profile">
        {userLoading ? (
          <p className="text-sm text-slate-500">Loading profile…</p>
        ) : user ? (
          <dl className="grid gap-4 sm:grid-cols-2">
            <div>
              <dt className="text-xs text-slate-500">Username</dt>
              <dd className="mt-0.5 text-ui text-slate-200">{user.username}</dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Email</dt>
              <dd className="mt-0.5 text-ui text-slate-200">{user.email || '—'}</dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Sign-in method</dt>
              <dd className="mt-0.5 text-ui text-slate-200">
                {user.auth_method === 'oidc' ? 'SSO (OIDC)' : 'Password'}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-slate-500">Platform role</dt>
              <dd className="mt-0.5 text-ui text-slate-200">
                {isSuperadmin ? 'Superadmin' : 'Member'}
              </dd>
            </div>
          </dl>
        ) : (
          <p className="text-sm text-slate-500">Profile unavailable.</p>
        )}
      </Panel>

      <ContactsPanel />

      <Panel
        title="Sessions"
        subtitle="Browsers signed in to your account. Signing one out ends its access immediately."
        actions={
          others > 0 ? (
            <Button size="xs" onClick={() => setPending({ kind: 'others' })}>
              Sign out other sessions
            </Button>
          ) : undefined
        }
      >
        {loading ? (
          <p className="text-sm text-slate-500">Loading sessions…</p>
        ) : error ? (
          <Callout
            tone="danger"
            action={<Button variant="ghost" size="xs" onClick={load}>Retry</Button>}
          >
            {error}
          </Callout>
        ) : sessions.length === 0 ? (
          <div className="flex flex-col items-center py-6 text-center">
            <MonitorSmartphone className="h-5 w-5 text-slate-500" strokeWidth={1.5} aria-hidden="true" />
            <p className="mt-3 text-sm font-medium text-white">No active sessions found</p>
            <p className="mt-1 text-ui text-slate-400">
              Session tracking may not be enabled on this server yet.
            </p>
          </div>
        ) : (
          <ul className="-mx-5 -mb-5 divide-y divide-divider">
            {sessions.map((s) => (
              <li
                key={s.id}
                className="flex flex-col gap-3 px-5 py-3.5 transition-colors hover:bg-well sm:flex-row sm:items-center sm:justify-between"
              >
                <div className="min-w-0">
                  <p className="flex flex-wrap items-center gap-2 text-sm font-medium text-white">
                    {describeUserAgent(s.user_agent)}
                    {s.current && (
                      <Pill tone="info" size="xs">
                        This browser
                      </Pill>
                    )}
                  </p>
                  <p className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-slate-500">
                    {s.ip && <span className="font-mono">{s.ip}</span>}
                    <span>{sessionMethodLabel(s.auth_method)}</span>
                    <span>Signed in {formatDateTime(s.created_at)}</span>
                    <span>Active {formatRelativeTime(s.last_active_at)}</span>
                  </p>
                </div>
                <div className="flex flex-shrink-0 items-center gap-2">
                  <IconButton
                    icon={<LogOut strokeWidth={1.75} />}
                    label={s.current ? 'Sign out this browser' : 'Sign out session'}
                    onClick={() => setPending({ kind: 'one', session: s })}
                    danger
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </Panel>

      <Panel
        title="Sign out everywhere"
        subtitle="Ends every session of your account, including this one. API keys and AI agent connections are not affected."
      >
        <Button variant="danger" onClick={() => setPending({ kind: 'all' })}>
          Sign out everywhere
        </Button>
      </Panel>

      <ConfirmDialog
        open={pending !== null}
        title={
          pending?.kind === 'all'
            ? 'Sign out everywhere?'
            : pending?.kind === 'others'
              ? 'Sign out other sessions?'
              : pending?.kind === 'one' && pending.session.current
                ? 'Sign out this browser?'
                : 'Sign out session?'
        }
        description={
          pending?.kind === 'all'
            ? 'Every browser signed in as you loses access immediately, this one included.'
            : pending?.kind === 'others'
              ? `${pluralize(others, 'other session')} will lose access immediately. This browser stays signed in.`
              : pending?.kind === 'one' && pending.session.current
                ? 'You will be returned to the sign-in page.'
                : 'That browser loses access immediately and must sign in again.'
        }
        confirmLabel="Sign out"
        tone="danger"
        loading={working}
        onConfirm={confirm}
        onCancel={() => setPending(null)}
      />
    </Page>
  );
}
