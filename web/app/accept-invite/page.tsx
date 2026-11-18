'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import Callout from '@/components/ui/Callout';
import AuthCard from '@/components/auth/AuthCard';
import SetPasswordForm from '@/components/auth/SetPasswordForm';
import { acceptInvite, getInviteInfo } from '@/lib/api';
import { linkErrorMessage, takeLinkToken } from '@/lib/account-links';
import { errorCode, errorMessage } from '@/lib/errors';
import { formatDateTime } from '@/lib/format';
import type { InviteInfo } from '@/lib/types';

// Behind an invite link (/accept-invite#<token>; lib/account-links.ts): names
// the account (reading the link does not use it) and sets its first password.
export default function AcceptInvitePage() {
  const [token, setToken] = useState<string | undefined>(undefined);
  const [invite, setInvite] = useState<InviteInfo | null>(null);
  const [loadError, setLoadError] = useState('');
  const [done, setDone] = useState(false);

  useEffect(() => {
    setToken(takeLinkToken());
  }, []);

  useEffect(() => {
    if (!token) return;
    getInviteInfo(token)
      .then(setInvite)
      .catch((err) => setLoadError(linkErrorMessage(errorCode(err), errorMessage(err, 'Could not read this invite. Try again.'))));
  }, [token]);

  const submit = async (password: string) => {
    try {
      await acceptInvite(token ?? '', password);
      setDone(true);
    } catch (err) {
      throw new Error(linkErrorMessage(errorCode(err), errorMessage(err, 'Setting the password failed. Try again.')));
    }
  };

  return (
    <AuthCard
      kicker="Invitation"
      title={invite ? `Welcome, ${invite.username}` : 'Set up your account'}
      subtitle={invite && !done ? 'Choose a password to finish setting up your Probara account.' : undefined}
    >
      {token === '' ? (
        <Callout tone="danger" title="Link not valid">
          This link is incomplete. Open the link from the invitation email again.
        </Callout>
      ) : loadError ? (
        <Callout tone="danger" title="Link not valid">
          {loadError} Ask the administrator who invited you to send a new invite.
        </Callout>
      ) : !invite ? (
        <p className="py-6 text-center text-sm text-slate-400">Loading…</p>
      ) : done ? (
        <Callout tone="success" title="Your account is ready" live="polite">
          Sign in as <span className="font-medium text-white">{invite.username}</span> with the password you chose.
        </Callout>
      ) : (
        <>
          <SetPasswordForm submitLabel="Set password" onSubmit={submit} username={invite.username} />
          <p className="mt-4 text-center text-xs text-slate-500">
            Your username is {invite.username}. This link works once and expires {formatDateTime(invite.expires_at)}.
          </p>
        </>
      )}
      {done && (
        <p className="mt-6 text-center text-sm">
          <Link href="/login" className="text-cyan-300 hover:text-cyan-200">
            Sign in
          </Link>
        </p>
      )}
    </AuthCard>
  );
}
