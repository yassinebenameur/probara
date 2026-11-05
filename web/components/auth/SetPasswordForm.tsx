'use client';

import { useState } from 'react';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';

const MIN_PASSWORD = 12;

/** New password + confirmation, for invite and reset links. */
export default function SetPasswordForm({
  submitLabel,
  onSubmit,
}: {
  submitLabel: string;
  onSubmit: (password: string) => Promise<void>;
}) {
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (password.length < MIN_PASSWORD) return setError(`Use at least ${MIN_PASSWORD} characters.`);
    if (password !== confirm) return setError('The passwords do not match.');
    setError('');
    setSaving(true);
    try {
      await onSubmit(password);
    } catch (err: any) {
      setError(err.message || 'Something went wrong. Please try again.');
      setSaving(false);
    }
  };

  return (
    <form noValidate onSubmit={submit} className="space-y-5">
      <div>
        <label htmlFor="new-password" className="mb-1.5 block text-ui font-medium text-slate-300">
          New password
        </label>
        <input
          id="new-password"
          type="password"
          autoComplete="new-password"
          autoFocus
          className="input"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          disabled={saving}
        />
        <p className="mt-1.5 text-xs text-slate-500">At least {MIN_PASSWORD} characters.</p>
      </div>
      <div>
        <label htmlFor="confirm-password" className="mb-1.5 block text-ui font-medium text-slate-300">
          Confirm password
        </label>
        <input
          id="confirm-password"
          type="password"
          autoComplete="new-password"
          className="input"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          disabled={saving}
        />
      </div>
      {error && <Callout tone="danger">{error}</Callout>}
      <Button type="submit" variant="accent" size="sm" loading={saving} disabled={saving} className="!h-10 w-full justify-center !text-sm">
        {submitLabel}
      </Button>
    </form>
  );
}
