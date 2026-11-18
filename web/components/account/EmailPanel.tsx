'use client';

import { useState } from 'react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import FormField from '@/components/ui/FormField';
import { changeMyEmail } from '@/lib/api';
import { errorMessage } from '@/lib/errors';
import { formatDateTime } from '@/lib/format';
import type { AccountSecurity } from '@/lib/types';

/**
 * Change the signed-in user's email address: a confirmation link goes to the
 * new address (and a notice to the current one); nothing changes until it is
 * used.
 */
export default function EmailPanel({ security, onChanged }: { security: AccountSecurity; onChanged: () => void }) {
  const [current, setCurrent] = useState('');
  const [email, setEmail] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  if (!security.local) {
    return (
      <Panel title="Email address">
        <Callout tone="info">
          You sign in with single sign-on: your identity provider owns your email address, and it links your sign-in
          to this account, so it cannot be changed here.
        </Callout>
      </Panel>
    );
  }

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email.trim() || !current) {
      setError('Enter the new address and your current password.');
      return;
    }
    setError('');
    setBusy(true);
    try {
      await changeMyEmail(current, email.trim());
      setCurrent('');
      setEmail('');
      onChanged();
    } catch (err) {
      setError(errorMessage(err, 'Requesting the change failed.'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel
      title="Email address"
      subtitle="Used for password reset links and security notices. A new address takes effect once you open the confirmation link sent to it (valid 24 hours)."
    >
      <dl className="mb-4">
        <dt className="text-xs text-slate-500">Current address</dt>
        <dd className="mt-0.5 text-ui text-slate-300">{security.email || 'None'}</dd>
      </dl>
      {security.pending_email && (
        <Callout tone="info" title="Waiting for confirmation" className="mb-4">
          A confirmation link was sent to {security.pending_email}. It expires{' '}
          {formatDateTime(security.pending_email_expires_at)}. Asking again replaces it; changing your password cancels
          it.
        </Callout>
      )}
      {!security.email_delivery ? (
        <Callout tone="info">
          This installation does not send email, so a new address cannot be confirmed. Ask a superadmin to change it
          under Users.
        </Callout>
      ) : (
        <form onSubmit={submit} className="max-w-md space-y-4" noValidate>
          <FormField label="New email address">
            <input
              type="email"
              className="input"
              autoComplete="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              disabled={busy}
            />
          </FormField>
          <FormField label="Current password">
            <input
              type="password"
              className="input"
              autoComplete="current-password"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
              disabled={busy}
            />
          </FormField>
          {error && <Callout tone="danger" live="polite">{error}</Callout>}
          <Button type="submit" variant="accent" size="sm" loading={busy} disabled={busy}>
            Send confirmation link
          </Button>
        </form>
      )}
    </Panel>
  );
}
