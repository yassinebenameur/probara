'use client';

import { useState } from 'react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import FormField from '@/components/ui/FormField';
import { PASSWORD_HINT, passwordProblem } from '@/lib/password';

/**
 * Choose a new password (twice). Used by the invite and reset-link pages; the
 * API applies the same rule (shared/auth.ValidatePassword).
 */
export default function SetPasswordForm({
  submitLabel,
  onSubmit,
  username,
}: {
  submitLabel: string;
  onSubmit: (password: string) => Promise<void>;
  /** Fills a hidden username field so password managers file the new password. */
  username?: string;
}) {
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const problem = passwordProblem(password, confirm);
    if (problem) {
      setError(problem);
      return;
    }
    setError('');
    setBusy(true);
    try {
      await onSubmit(password);
    } catch (err) {
      setError(err instanceof Error && err.message ? err.message : 'Setting the password failed. Try again.');
    } finally {
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="space-y-5" noValidate>
      {username && <input type="text" name="username" autoComplete="username" value={username} readOnly hidden />}
      <FormField label="New password" description={PASSWORD_HINT}>
        <input
          type="password"
          className="input"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          disabled={busy}
          required
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
          required
        />
      </FormField>
      {error && <Callout tone="danger" live="polite">{error}</Callout>}
      <Button
        type="submit"
        variant="accent"
        size="sm"
        loading={busy}
        disabled={busy}
        className="!h-10 w-full justify-center !text-sm"
      >
        {submitLabel}
      </Button>
    </form>
  );
}
