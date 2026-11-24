'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import FormField from '@/components/ui/FormField';
import AuthCard from '@/components/auth/AuthCard';
import { getAuthCapabilities, requestPasswordReset } from '@/lib/api';
import { errorMessage } from '@/lib/errors';

// The public forgot-password form. The API answers every request the same
// way, whether or not the account exists, so this page says the same thing
// in every case: "if an account matches, a link is on its way". Without email
// delivery on this installation nothing is sent, and the page says to ask an
// administrator instead -- again without saying anything about the account.
export default function ForgotPasswordPage() {
  const [emailDelivery, setEmailDelivery] = useState<boolean | null>(null);
  const [login, setLogin] = useState('');
  const [sent, setSent] = useState(false);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    getAuthCapabilities().then((caps) => setEmailDelivery(caps.email_delivery));
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!login.trim()) {
      setError('Enter your username or email address.');
      return;
    }
    setError('');
    setBusy(true);
    try {
      await requestPasswordReset(login.trim());
      setSent(true);
    } catch (err) {
      setError(errorMessage(err, 'The request failed. Try again.'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <AuthCard kicker="Sign-in help" title="Forgot your password?">
      {emailDelivery === null ? (
        <p className="py-6 text-center text-sm text-slate-400">Loading…</p>
      ) : !emailDelivery ? (
        <Callout tone="info" title="Ask an administrator">
          This installation does not send email, so password reset links come from a Probara administrator: ask one to
          generate a reset link for your account. If you sign in with single sign-on, your identity provider manages
          your password.
        </Callout>
      ) : sent ? (
        <Callout tone="success" title="Check your email" live="polite">
          If an account with a password matches what you entered and has an email address, a reset link is on its way.
          It works once and expires in one hour. Accounts that sign in with single sign-on get no link: their password
          is managed by the identity provider.
        </Callout>
      ) : (
        <form onSubmit={submit} className="space-y-5" noValidate>
          <FormField label="Username or email address">
            <input
              type="text"
              className="input"
              autoComplete="username"
              value={login}
              onChange={(e) => setLogin(e.target.value)}
              disabled={busy}
              required
            />
          </FormField>
          {error && <Callout tone="danger">{error}</Callout>}
          <Button
            type="submit"
            variant="accent"
            size="sm"
            loading={busy}
            disabled={busy}
            className="!h-10 w-full justify-center !text-sm"
          >
            Email me a reset link
          </Button>
        </form>
      )}
      <p className="mt-6 text-center text-sm">
        <Link href="/login" className="text-cyan-300 hover:text-cyan-200">
          Back to sign in
        </Link>
      </p>
    </AuthCard>
  );
}
