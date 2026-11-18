'use client';

import { useState } from 'react';
import { KeyRound, ShieldCheck, ShieldOff } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import CopyableTarget from '@/components/ui/CopyableTarget';
import FormField from '@/components/ui/FormField';
import { useToast } from '@/components/ui/ToastProvider';
import QrCode from './QrCode';
import RecoveryCodes from './RecoveryCodes';
import { disableTwoFactor, enableTwoFactor, regenerateRecoveryCodes, startTwoFactorSetup } from '@/lib/api';
import { groupSecret } from '@/lib/account-links';
import { errorMessage } from '@/lib/errors';
import { formatDateTime, pluralize } from '@/lib/format';
import type { AccountSecurity, TotpSetupResponse } from '@/lib/types';

type Step =
  | { kind: 'idle' }
  | { kind: 'password' }
  | { kind: 'scan'; setup: TotpSetupResponse }
  | { kind: 'codes'; codes: string[] }
  | { kind: 'proof'; action: 'disable' | 'regenerate' };

/**
 * Optional two-factor sign-in with an authenticator app (TOTP), for password
 * accounts. Setup re-checks the password, shows a QR code drawn in the
 * browser plus the key for manual entry, and is confirmed with a code; then
 * ten single-use recovery codes are shown once. Turning it off or replacing
 * the recovery codes needs the password and a current code (or a recovery
 * code).
 */
export default function TwoFactorPanel({
  security,
  username,
  onChanged,
}: {
  security: AccountSecurity;
  username: string;
  onChanged: () => void;
}) {
  const { showToast } = useToast();
  const [step, setStep] = useState<Step>({ kind: 'idle' });
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const tf = security.two_factor;

  const reset = (next: Step = { kind: 'idle' }) => {
    setStep(next);
    setPassword('');
    setCode('');
    setError('');
  };

  const run = async (action: () => Promise<void>, fallback: string) => {
    setBusy(true);
    setError('');
    try {
      await action();
    } catch (err) {
      setError(errorMessage(err, fallback));
    } finally {
      setBusy(false);
    }
  };

  if (!security.local && !tf.enabled) {
    return (
      <Panel title="Two-factor sign-in">
        <Callout tone="info">
          You sign in with single sign-on: your identity provider handles multi-factor authentication. Ask your
          administrator how it is set up there.
        </Callout>
      </Panel>
    );
  }

  if (step.kind === 'codes') {
    return (
      <Panel title="Recovery codes">
        <RecoveryCodes
          username={username}
          codes={step.codes}
          onDone={() => {
            reset();
            onChanged();
          }}
        />
      </Panel>
    );
  }

  const errorCallout = error ? <Callout tone="danger" live="polite">{error}</Callout> : null;
  const cancel = (
    <Button variant="ghost" size="sm" onClick={() => reset()} disabled={busy}>
      Cancel
    </Button>
  );

  if (!tf.enabled) {
    return (
      <Panel
        title="Two-factor sign-in"
        subtitle="Off. When on, signing in with your password also asks for a code from an authenticator app, or one of your recovery codes."
      >
        {!tf.enrolment_available ? (
          <Callout tone="info">
            Two-factor sign-in is not available on this installation yet: the API needs PROBARA_SECRETS_KEY to store the
            authenticator secret encrypted. Ask your administrator.
          </Callout>
        ) : step.kind === 'idle' ? (
          <Button variant="accent" size="sm" icon={<ShieldCheck />} onClick={() => reset({ kind: 'password' })}>
            Set up two-factor sign-in
          </Button>
        ) : step.kind === 'password' ? (
          <form
            className="max-w-md space-y-4"
            noValidate
            onSubmit={(e) => {
              e.preventDefault();
              void run(async () => {
                const setup = await startTwoFactorSetup(password);
                reset({ kind: 'scan', setup });
              }, 'Starting the setup failed.');
            }}
          >
            <FormField label="Current password">
              <input
                type="password"
                className="input"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={busy}
              />
            </FormField>
            {errorCallout}
            <div className="flex gap-2">
              <Button type="submit" variant="accent" size="sm" loading={busy} disabled={busy || !password}>
                Continue
              </Button>
              {cancel}
            </div>
          </form>
        ) : step.kind === 'scan' ? (
          <div className="space-y-5">
            <div className="flex flex-col gap-5 sm:flex-row sm:items-start">
              <QrCode value={step.setup.otpauth_uri} label="QR code to add Probara to an authenticator app" />
              <div className="min-w-0 space-y-3 text-sm text-slate-400">
                <p>Scan the code with an authenticator app (1Password, Google Authenticator, Authy, …).</p>
                <div>
                  <p className="text-xs text-slate-500">Or enter this key by hand (time-based, 6 digits, 30 seconds):</p>
                  <CopyableTarget value={groupSecret(step.setup.secret)} label="key" href={null} className="mt-1" />
                </div>
              </div>
            </div>
            <form
              className="max-w-xs space-y-4"
              noValidate
              onSubmit={(e) => {
                e.preventDefault();
                void run(async () => {
                  const { recovery_codes } = await enableTwoFactor(code);
                  showToast('Two-factor sign-in is on', 'success');
                  reset({ kind: 'codes', codes: recovery_codes });
                }, 'Turning on two-factor sign-in failed.');
              }}
            >
              <FormField label="Code from the app">
                <input
                  type="text"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  className="input font-mono tracking-widest"
                  maxLength={8}
                  value={code}
                  onChange={(e) => setCode(e.target.value)}
                  disabled={busy}
                />
              </FormField>
              {errorCallout}
              <div className="flex gap-2">
                <Button type="submit" variant="accent" size="sm" loading={busy} disabled={busy || !code.trim()}>
                  Turn on
                </Button>
                {cancel}
              </div>
            </form>
          </div>
        ) : null}
      </Panel>
    );
  }

  const proof = step.kind === 'proof' ? step.action : null;
  return (
    <Panel
      title="Two-factor sign-in"
      subtitle="On. Signing in with your password also asks for a code from your authenticator app, or a recovery code."
    >
      <dl className="mb-4 grid gap-4 sm:grid-cols-2">
        <div>
          <dt className="text-xs text-slate-500">Turned on</dt>
          <dd className="mt-0.5 text-ui text-slate-300">{formatDateTime(tf.enabled_at, '—')}</dd>
        </div>
        <div>
          <dt className="text-xs text-slate-500">Recovery codes</dt>
          <dd className="mt-0.5 text-ui text-slate-300">{pluralize(tf.recovery_codes_remaining, 'unused code')}</dd>
        </div>
      </dl>
      {tf.recovery_codes_remaining <= 2 && !proof && (
        <Callout tone="warning" className="mb-4">
          Few recovery codes left. Generate a new set so you can still sign in if you lose your authenticator.
        </Callout>
      )}
      {!proof ? (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" icon={<KeyRound />} onClick={() => reset({ kind: 'proof', action: 'regenerate' })}>
            New recovery codes
          </Button>
          <Button variant="danger" size="sm" icon={<ShieldOff />} onClick={() => reset({ kind: 'proof', action: 'disable' })}>
            Turn off
          </Button>
        </div>
      ) : (
        <form
          className="max-w-md space-y-4"
          noValidate
          onSubmit={(e) => {
            e.preventDefault();
            if (proof === 'disable') {
              void run(async () => {
                const { revoked } = await disableTwoFactor(password, code);
                showToast(
                  revoked > 0
                    ? `Two-factor sign-in is off; ${pluralize(revoked, 'other session')} signed out`
                    : 'Two-factor sign-in is off',
                  'success'
                );
                reset();
                onChanged();
              }, 'Turning off two-factor sign-in failed.');
            } else {
              void run(async () => {
                const { recovery_codes } = await regenerateRecoveryCodes(password, code);
                reset({ kind: 'codes', codes: recovery_codes });
              }, 'Generating new recovery codes failed.');
            }
          }}
        >
          <Callout tone={proof === 'disable' ? 'warning' : 'info'}>
            {proof === 'disable'
              ? 'Turning it off signs out every other browser signed in to your account, and removes your recovery codes.'
              : 'New codes replace all of your current recovery codes, used or not.'}
          </Callout>
          <FormField label="Current password">
            <input
              type="password"
              className="input"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={busy}
            />
          </FormField>
          <FormField label="Code from the app, or a recovery code">
            <input
              type="text"
              autoComplete="one-time-code"
              className="input font-mono"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              disabled={busy}
            />
          </FormField>
          {errorCallout}
          <div className="flex gap-2">
            <Button
              type="submit"
              variant={proof === 'disable' ? 'danger' : 'accent'}
              size="sm"
              loading={busy}
              disabled={busy || !password || !code.trim()}
            >
              {proof === 'disable' ? 'Turn off two-factor sign-in' : 'Generate new codes'}
            </Button>
            {cancel}
          </div>
        </form>
      )}
    </Panel>
  );
}
