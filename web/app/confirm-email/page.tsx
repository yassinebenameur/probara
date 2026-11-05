'use client';

import { useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import AuthCard from '@/components/auth/AuthCard';
import Callout from '@/components/ui/Callout';
import { confirmEmailChange } from '@/lib/api';
import { readLinkToken } from '@/lib/account-links';

type State = { kind: 'working' } | { kind: 'done'; email: string } | { kind: 'failed'; message: string };

/** /confirm-email#token=…: completes an email change from the link sent to the new address. */
export default function ConfirmEmailPage() {
  const [state, setState] = useState<State>({ kind: 'working' });
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const token = readLinkToken(window.location.hash);
    if (!token) {
      setState({ kind: 'failed', message: 'This link is incomplete. Open the link from your email again.' });
      return;
    }
    confirmEmailChange(token)
      .then((res) => {
        window.history.replaceState(null, '', window.location.pathname);
        setState({ kind: 'done', email: res.email });
      })
      .catch((err: any) =>
        setState({
          kind: 'failed',
          message:
            err?.error === 'invalid_token'
              ? 'This confirmation link is invalid, was already used or has expired. Request the change again from your account page.'
              : err?.message || 'The email address could not be changed.',
        })
      );
  }, []);

  if (state.kind === 'working') {
    return (
      <AuthCard title="Confirming your email">
        <p className="text-center text-sm text-slate-400">One moment…</p>
      </AuthCard>
    );
  }
  if (state.kind === 'failed') {
    return (
      <AuthCard title="Email not changed">
        <Callout tone="danger">{state.message}</Callout>
      </AuthCard>
    );
  }
  return (
    <AuthCard title="Email confirmed" subtitle={<>Your email address is now <span className="font-medium text-slate-200">{state.email}</span>.</>}>
      <Link href="/account" className="block text-center text-sm font-medium text-cyan-300 hover:text-cyan-200">
        Go to your account
      </Link>
    </AuthCard>
  );
}
