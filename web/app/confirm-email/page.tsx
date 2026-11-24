'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import AuthCard from '@/components/auth/AuthCard';
import { confirmEmailChange } from '@/lib/api';
import { linkErrorMessage, takeLinkToken } from '@/lib/account-links';
import { errorCode, errorMessage } from '@/lib/errors';

// Behind the confirmation link mailed to a new address
// (/confirm-email#<token>; lib/account-links.ts). Confirming is the button's
// POST, never automatic on load: mail scanners open every link they see.
export default function ConfirmEmailPage() {
  const [token, setToken] = useState<string | undefined>(undefined);
  const [email, setEmail] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setToken(takeLinkToken());
  }, []);

  const confirm = async () => {
    setBusy(true);
    setError('');
    try {
      const res = await confirmEmailChange(token ?? '');
      setEmail(res.email);
    } catch (err) {
      setError(linkErrorMessage(errorCode(err), errorMessage(err, 'Confirming failed. Try again.')));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthCard kicker="Email address" title="Confirm your new email address">
      {token === undefined ? (
        <p className="py-6 text-center text-sm text-slate-400">Loading…</p>
      ) : token === '' ? (
        <Callout tone="danger" title="Link not valid">
          This link is incomplete. Open the link from the confirmation email again.
        </Callout>
      ) : email ? (
        <Callout tone="success" title="Email address changed" live="polite">
          Your Probara account now uses {email}.
        </Callout>
      ) : (
        <div className="space-y-5">
          <p className="text-sm text-slate-400">
            Confirm to make this address the one your Probara account uses for sign-in help and notices.
          </p>
          {error && <Callout tone="danger">{error}</Callout>}
          <Button
            variant="accent"
            size="sm"
            className="!h-10 w-full justify-center !text-sm"
            loading={busy}
            disabled={busy}
            onClick={confirm}
          >
            Confirm email address
          </Button>
        </div>
      )}
      <p className="mt-6 text-center text-sm">
        <Link href="/account" className="text-cyan-300 hover:text-cyan-200">
          Go to your account
        </Link>
      </p>
    </AuthCard>
  );
}
