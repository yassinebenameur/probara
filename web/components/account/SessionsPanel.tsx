'use client';

import { useCallback, useState } from 'react';
import { LogOut, MonitorSmartphone } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { RelativeTime } from '@/components/ui/RelativeTime';
import { useToast } from '@/components/ui/ToastProvider';
import { sessionClientLabel, sessionMethodLabel, sortSessions } from '@/lib/account';
import { formatDateTime, pluralize } from '@/lib/format';
import { errorMessage } from '@/lib/errors';
import { useAsyncData } from '@/lib/useAsyncData';
import type { AdminSession, RevokeSessionsResponse } from '@/lib/types';

interface SessionsPanelProps {
  /** Whose sessions: the signed-in user's own (account page) or another user's. */
  scope: 'self' | 'user';
  /** The user's name, for the superadmin view's wording. */
  username?: string;
  load: () => Promise<AdminSession[]>;
  revoke: (id: string) => Promise<void>;
  /** Self: every session but this one. User: all of them. */
  revokeAll: () => Promise<RevokeSessionsResponse>;
}

/**
 * A user's signed-in sessions with sign-out controls. Revoking takes effect
 * on the session's next request; it never signs anyone out of the SSO
 * identity provider. API keys and MCP connections are not sessions.
 */
export default function SessionsPanel({ scope, username, load, revoke, revokeAll }: SessionsPanelProps) {
  const { showToast } = useToast();
  const loadSorted = useCallback(async () => sortSessions(await load()), [load]);
  const { data: sessions, loading, error, reload } = useAsyncData<AdminSession[]>(
    loadSorted,
    [],
    'Failed to load sessions'
  );
  const [pending, setPending] = useState<AdminSession | 'all' | null>(null);
  const [busy, setBusy] = useState(false);

  const self = scope === 'self';
  // "All" never includes the session in use (the API keeps it, too).
  const others = sessions.filter((s) => !s.current);
  const keepsCurrent = self || sessions.some((s) => s.current);
  const allLabel = keepsCurrent ? 'Sign out everywhere else' : 'Sign out everywhere';

  const confirm = async () => {
    if (!pending) return;
    setBusy(true);
    try {
      if (pending === 'all') {
        const { revoked } = await revokeAll();
        showToast(`Signed out ${pluralize(revoked, 'session')}`, 'success');
      } else {
        await revoke(pending.id);
        showToast('Session signed out', 'success');
      }
      setPending(null);
      await reload();
    } catch (err) {
      showToast(errorMessage(err, 'Failed to sign out the session'), 'error');
    } finally {
      setBusy(false);
    }
  };

  const subtitle = self
    ? 'Browsers signed in to your account. Signing one out takes effect on its next request. API keys (Settings → Access) and MCP connections (Settings → MCP) are managed separately.'
    : `Browsers signed in as ${username ?? 'this user'}. Signing one out takes effect on its next request.`;

  return (
    <>
      <Panel
        title="Sessions"
        subtitle={subtitle}
        actions={
          others.length > 0 ? (
            <Button variant="danger" size="xs" icon={<LogOut />} onClick={() => setPending('all')}>
              {allLabel}
            </Button>
          ) : undefined
        }
      >
        {loading ? (
          <p className="text-sm text-slate-500">Loading sessions…</p>
        ) : error ? (
          <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={reload}>Retry</Button>}>
            {error}
          </Callout>
        ) : sessions.length === 0 ? (
          <div className="flex flex-col items-center py-6 text-center">
            <MonitorSmartphone className="h-5 w-5 text-slate-500" strokeWidth={1.5} aria-hidden="true" />
            <p className="mt-3 text-sm font-medium text-white">No active sessions</p>
            <p className="mt-1 text-ui text-slate-400">
              {self ? 'Sessions appear here after you sign in.' : 'This user is not signed in anywhere.'}
            </p>
          </div>
        ) : (
          <ul className="-mx-5 -mb-5 divide-y divide-divider">
            {sessions.map((s) => {
              const method = sessionMethodLabel(s.auth_method);
              return (
                <li
                  key={s.id}
                  className="flex flex-col gap-3 px-5 py-3.5 transition-colors hover:bg-well sm:flex-row sm:items-center sm:justify-between"
                >
                  <div className="min-w-0">
                    <p className="flex items-center gap-2 text-sm font-medium text-white">
                      <span className="truncate" title={s.user_agent || undefined}>
                        {sessionClientLabel(s)}
                      </span>
                      {s.current && (
                        <Pill tone="info" size="xs">
                          {self ? 'This device' : 'Your session'}
                        </Pill>
                      )}
                    </p>
                    <p className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-slate-500">
                      {method && <span>{method}</span>}
                      {s.ip && <span className="font-mono">{s.ip}</span>}
                      <span>Signed in {formatDateTime(s.created_at)}</span>
                      <span>
                        <RelativeTime value={s.last_used_at} prefix="Active " />
                      </span>
                    </p>
                  </div>
                  {/* The session in use signs out from the sidebar, not here. */}
                  {!s.current && (
                    <div className="flex flex-shrink-0 items-center gap-2">
                      <IconButton
                        icon={<LogOut strokeWidth={1.75} />}
                        label="Sign out this session"
                        onClick={() => setPending(s)}
                        danger
                      />
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </Panel>

      <ConfirmDialog
        open={pending !== null}
        title={pending === 'all' ? `${allLabel}?` : 'Sign out this session?'}
        description={
          pending === 'all'
            ? keepsCurrent
              ? 'Every other browser signed in to your account is signed out on its next request. This one stays signed in. Single sign-on sessions at your identity provider are not affected.'
              : `${username ?? 'This user'} is signed out of every browser on its next request. Single sign-on sessions at the identity provider are not affected.`
            : 'That browser is signed out on its next request. A single sign-on session at the identity provider is not affected, so it may sign in again without a password.'
        }
        confirmLabel="Sign out"
        tone="danger"
        loading={busy}
        onConfirm={confirm}
        onCancel={() => setPending(null)}
      />
    </>
  );
}
