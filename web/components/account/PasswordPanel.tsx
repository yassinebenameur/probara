'use client';

import { useState } from 'react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import FormField from '@/components/ui/FormField';
import { useToast } from '@/components/ui/ToastProvider';
import { changeMyPassword } from '@/lib/api';
import { errorMessage } from '@/lib/errors';
import { pluralize } from '@/lib/format';
import { PASSWORD_HINT, passwordProblem } from '@/lib/password';
import type { AccountSecurity } from '@/lib/types';

/** Change the signed-in user's password; other browsers are signed out. */
export default function PasswordPanel({ security }: { security: AccountSecurity }) {
  const { showToast } = useToast();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  if (!security.local) {
    return (
      <Panel title="Password">
        <Callout tone="info">
          You sign in with single sign-on, so your identity provider manages your password. Change it there.
        </Callout>
      </Panel>
    );
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const problem = !current ? 'Enter your current password.' : passwordProblem(next, confirm);
    if (problem) {
      setError(problem);
      return;
    }
    setError('');
    setBusy(true);
    try {
      const { revoked } = await changeMyPassword(current, next);
      setCurrent('');
      setNext('');
      setConfirm('');
      showToast(
        revoked > 0 ? `Password changed; ${pluralize(revoked, 'other session')} signed out` : 'Password changed',
        'success'
      );
    } catch (err) {
      setError(errorMessage(err, 'Changing the password failed.'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel
      title="Password"
      subtitle="Changing it signs out every other browser signed in to your account and cancels open reset links and a pending email change. This one stays signed in."
    >
      <form onSubmit={submit} className="max-w-md space-y-4" noValidate>
        <FormField label="Current password" description="Wrong attempts count toward the sign-in lockout (10 in a row lock the account for 15 minutes).">
          <input
            type="password"
            className="input"
            autoComplete="current-password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            disabled={busy}
          />
        </FormField>
        <FormField label="New password" description={PASSWORD_HINT}>
          <input
            type="password"
            className="input"
            autoComplete="new-password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            disabled={busy}
          />
        </FormField>
        <FormField label="Confirm the new password">
          <input
            type="password"
            className="input"
            autoComplete="new-password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            disabled={busy}
          />
        </FormField>
        {error && <Callout tone="danger" live="polite">{error}</Callout>}
        <Button type="submit" variant="accent" size="sm" loading={busy} disabled={busy}>
          Change password
        </Button>
      </form>
      {security.two_factor.enabled && (
        <p className="mt-4 text-xs text-slate-500">
          Changing the password leaves two-factor sign-in on ({pluralize(security.two_factor.recovery_codes_remaining, 'recovery code')} left).
        </p>
      )}
    </Panel>
  );
}
