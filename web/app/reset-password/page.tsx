'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import Callout from '@/components/ui/Callout';
import AuthCard from '@/components/auth/AuthCard';
import SetPasswordForm from '@/components/auth/SetPasswordForm';
import { resetPasswordWithLink } from '@/lib/api';
import { linkErrorMessage, takeLinkToken } from '@/lib/account-links';
import { errorCode, errorMessage } from '@/lib/errors';

// Behind a password reset link (/reset-password#<token>; lib/account-links.ts).
// Setting the password signs the account out of every browser and revokes
// its MCP connections (workspace API keys stay); two-factor sign-in,
// when on, stays on.
export default function ResetPasswordPage() {
  // undefined until the fragment has been read on the client.
  const [token, setToken] = useState<string | undefined>(undefined);
  const [done, setDone] = useState(false);

  useEffect(() => {
    setToken(takeLinkToken());
  }, []);

  const submit = async (password: string) => {
    try {
      await resetPasswordWithLink(token ?? '', password);
      setDone(true);
    } catch (err) {
      throw new Error(linkErrorMessage(errorCode(err), errorMessage(err, 'Setting the password failed. Try again.')));
    }
  };

  return (
    <AuthCard kicker="Password reset" title="Choose a new password">
      {token === undefined ? (
        <p className="py-6 text-center text-sm text-slate-400">Loading…</p>
      ) : token === '' ? (
        <Callout tone="danger" title="Link not valid">
          This link is incomplete. Open the link from the email again, or ask for a new one.
        </Callout>
      ) : done ? (
        <Callout tone="success" title="Password changed" live="polite">
          Every browser signed in to the account was signed out and its MCP connections were revoked (workspace API keys are not affected). Sign in with the new password.
        </Callout>
      ) : (
        <SetPasswordForm submitLabel="Set the new password" onSubmit={submit} />
      )}
      <p className="mt-6 text-center text-sm">
        <Link href={done ? '/login' : '/forgot-password'} className="text-cyan-300 hover:text-cyan-200">
          {done ? 'Sign in' : 'Need a new link?'}
        </Link>
      </p>
    </AuthCard>
  );
}
