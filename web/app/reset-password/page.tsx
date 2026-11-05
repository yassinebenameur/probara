'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import AuthCard from '@/components/auth/AuthCard';
import SetPasswordForm from '@/components/auth/SetPasswordForm';
import Callout from '@/components/ui/Callout';
import { resetPassword } from '@/lib/api';
import { readLinkToken } from '@/lib/account-links';

/**
 * /reset-password#token=…: choose a new password from a reset link. Saving
 * signs the account out everywhere; two-factor stays on.
 */
export default function ResetPasswordPage() {
  const [token, setToken] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  useEffect(() => {
    setToken(readLinkToken(window.location.hash));
  }, []);

  if (done) {
    return (
      <AuthCard title="Password changed" subtitle="Every session of your account was signed out.">
        <Link href="/login" className="block text-center text-sm font-medium text-cyan-300 hover:text-cyan-200">
          Sign in with your new password
        </Link>
      </AuthCard>
    );
  }
  if (token === null) {
    return (
      <AuthCard title="Reset password">
        <p className="text-center text-sm text-slate-400">Loading…</p>
      </AuthCard>
    );
  }
  if (!token) {
    return (
      <AuthCard title="Reset link not valid">
        <Callout tone="danger">This link is incomplete. Open the link from your email again.</Callout>
      </AuthCard>
    );
  }
  return (
    <AuthCard title="Choose a new password">
      <SetPasswordForm
        submitLabel="Change password"
        onSubmit={async (password) => {
          try {
            await resetPassword(token, password);
          } catch (err: any) {
            if (err?.error === 'invalid_token') {
              throw new Error('This reset link is invalid, was already used or has expired. Ask for a new one from the sign-in page.');
            }
            throw err;
          }
          window.history.replaceState(null, '', window.location.pathname);
          setDone(true);
        }}
      />
    </AuthCard>
  );
}
