'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import AuthCard from '@/components/auth/AuthCard';
import SetPasswordForm from '@/components/auth/SetPasswordForm';
import Callout from '@/components/ui/Callout';
import { acceptInvite, inspectInvite } from '@/lib/api';
import { readLinkToken } from '@/lib/account-links';
import type { InviteInfo } from '@/lib/types';

type State = { kind: 'loading' } | { kind: 'invalid' } | { kind: 'ready'; info: InviteInfo } | { kind: 'done' };

/**
 * /accept-invite#token=…: set the password of an invited account. The token
 * stays in the fragment (never sent to a server) and goes to the API in a body.
 */
export default function AcceptInvitePage() {
  const [token, setToken] = useState('');
  const [state, setState] = useState<State>({ kind: 'loading' });

  useEffect(() => {
    const t = readLinkToken(window.location.hash);
    setToken(t);
    if (!t) {
      setState({ kind: 'invalid' });
      return;
    }
    inspectInvite(t)
      .then((info) => setState({ kind: 'ready', info }))
      .catch(() => setState({ kind: 'invalid' }));
  }, []);

  if (state.kind === 'done') {
    return (
      <AuthCard title="You're all set" subtitle="Your password is saved.">
        <Link href="/login" className="block text-center text-sm font-medium text-cyan-300 hover:text-cyan-200">
          Continue to sign in
        </Link>
      </AuthCard>
    );
  }
  if (state.kind === 'invalid') {
    return (
      <AuthCard title="Invite link not valid">
        <Callout tone="danger">
          This invite link is invalid, was already used or has expired. Ask your administrator for a new one.
        </Callout>
      </AuthCard>
    );
  }
  if (state.kind === 'loading') {
    return (
      <AuthCard title="Accept invite">
        <p className="text-center text-sm text-slate-400">Checking your invite…</p>
      </AuthCard>
    );
  }
  return (
    <AuthCard
      title="Welcome to Probara"
      subtitle={<>Choose a password for <span className="font-medium text-slate-200">{state.info.username}</span>.</>}
    >
      <SetPasswordForm
        submitLabel="Set password"
        onSubmit={async (password) => {
          await acceptInvite(token, password);
          window.history.replaceState(null, '', window.location.pathname);
          setState({ kind: 'done' });
        }}
      />
    </AuthCard>
  );
}
