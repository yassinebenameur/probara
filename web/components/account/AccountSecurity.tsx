'use client';

import { useCallback, useEffect, useState } from 'react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import FormField from '@/components/ui/FormField';
import Input from '@/components/ui/Input';
import { useToast } from '@/components/ui/ToastProvider';
import QrCode from '@/components/account/QrCode';
import RecoveryCodes from '@/components/account/RecoveryCodes';
import {
  changeMyPassword,
  disableMyTOTP,
  enableMyTOTP,
  getAuthCapabilities,
  getMyMFA,
  regenerateMyRecoveryCodes,
  requestMyEmailChange,
  setupMyTOTP,
} from '@/lib/api';
import { formatDateTime, pluralize } from '@/lib/format';
import type { AccountMFAStatus, AdminUser, AuthCapabilities, TOTPSetup } from '@/lib/types';

const MIN_PASSWORD = 12;

/**
 * Password, email and two-factor for the signed-in user. Local password
 * accounts only: an SSO account's password, email and MFA belong to the
 * identity provider (the email is what links the identity), so they get a
 * note instead of forms.
 */
export default function AccountSecurity({
  user,
  onSessionsChanged,
}: {
  user: AdminUser;
  onSessionsChanged: () => void;
}) {
  const [mfa, setMfa] = useState<AccountMFAStatus | null>(null);
  const [caps, setCaps] = useState<AuthCapabilities | null>(null);
  const [error, setError] = useState('');
  const [dialog, setDialog] = useState<'password' | 'email' | 'setup' | 'disable' | 'codes' | null>(null);

  const load = useCallback(async () => {
    try {
      setError('');
      const [status, capabilities] = await Promise.all([getMyMFA(), getAuthCapabilities()]);
      setMfa(status);
      setCaps(capabilities);
    } catch (err: any) {
      setError(err.message || 'Failed to load sign-in settings');
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  if (error) {
    return (
      <Panel title="Sign-in and security">
        <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={load}>Retry</Button>}>
          {error}
        </Callout>
      </Panel>
    );
  }
  if (!mfa || !caps) {
    return (
      <Panel title="Sign-in and security">
        <p className="text-sm text-slate-500">Loading…</p>
      </Panel>
    );
  }
  if (!mfa.eligible) {
    return (
      <Panel title="Sign-in and security">
        <Callout tone="info">
          You sign in through single sign-on. Your password, email address and multi-factor
          authentication are managed by your identity provider.
        </Callout>
      </Panel>
    );
  }

  const close = () => setDialog(null);

  return (
    <>
      <Panel title="Sign-in and security" subtitle="Your password, the address account mail goes to, and two-factor authentication.">
        <ul className="-mx-5 -mb-5 divide-y divide-divider">
          <Row
            title="Password"
            detail="Changing it signs out your other sessions."
            action={<Button size="xs" onClick={() => setDialog('password')}>Change password</Button>}
          />
          <Row
            title="Email"
            detail={
              caps.email_delivery
                ? `${user.email || 'No email address'} · a change is confirmed through a link sent to the new address.`
                : `${user.email || 'No email address'} · email delivery is not configured here, so ask a superadmin to change it.`
            }
            action={
              <Button size="xs" disabled={!caps.email_delivery} onClick={() => setDialog('email')}>
                Change email
              </Button>
            }
          />
          <Row
            title={
              <span className="flex items-center gap-2">
                Two-factor authentication
                <Pill tone={mfa.enabled ? 'success' : 'neutral'} size="xs">{mfa.enabled ? 'On' : 'Off'}</Pill>
              </span>
            }
            detail={
              !mfa.available
                ? 'Not available until the server is upgraded.'
                : mfa.enabled
                  ? `Authenticator app since ${formatDateTime(mfa.enabled_at)} · ${pluralize(mfa.recovery_codes_remaining, 'recovery code')} left.`
                  : 'Ask for a code from an authenticator app at every password sign-in.'
            }
            action={
              !mfa.available ? undefined : mfa.enabled ? (
                <div className="flex gap-2">
                  <Button size="xs" onClick={() => setDialog('codes')}>New recovery codes</Button>
                  <Button size="xs" variant="danger" onClick={() => setDialog('disable')}>Turn off</Button>
                </div>
              ) : (
                <Button size="xs" variant="accent" onClick={() => setDialog('setup')}>Set up</Button>
              )
            }
          />
        </ul>
      </Panel>

      {dialog === 'password' && (
        <PasswordDialog
          onClose={close}
          onDone={() => {
            close();
            onSessionsChanged();
          }}
        />
      )}
      {dialog === 'email' && <EmailDialog onClose={close} />}
      {dialog === 'setup' && (
        <SetupDialog
          username={user.username}
          onClose={() => {
            close();
            load();
          }}
        />
      )}
      {(dialog === 'disable' || dialog === 'codes') && (
        <ProofDialog
          mode={dialog}
          username={user.username}
          onClose={() => {
            close();
            load();
          }}
        />
      )}
    </>
  );
}

function Row({ title, detail, action }: { title: React.ReactNode; detail: string; action?: React.ReactNode }) {
  return (
    <li className="flex flex-col gap-3 px-5 py-3.5 sm:flex-row sm:items-center sm:justify-between">
      <div className="min-w-0">
        <p className="text-sm font-medium text-white">{title}</p>
        <p className="mt-0.5 text-xs text-slate-500">{detail}</p>
      </div>
      {action && <div className="flex-shrink-0">{action}</div>}
    </li>
  );
}

function PasswordDialog({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { showToast } = useToast();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const save = async () => {
    if (next.length < MIN_PASSWORD) return setError(`Use at least ${MIN_PASSWORD} characters.`);
    if (next !== confirm) return setError('The new passwords do not match.');
    setSaving(true);
    setError('');
    try {
      const res = await changeMyPassword(current, next);
      showToast(
        res.revoked > 0 ? `Password changed; signed out ${pluralize(res.revoked, 'other session')}` : 'Password changed',
        'success'
      );
      onDone();
    } catch (err: any) {
      setError(err.message || 'Failed to change the password');
      setSaving(false);
    }
  };

  return (
    <Dialog
      title="Change password"
      description="Your other sessions are signed out; this browser stays signed in."
      onClose={onClose}
      busy={saving}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={saving}>Cancel</Button>
          <Button variant="accent" onClick={save} loading={saving}>Change password</Button>
        </>
      }
    >
      <form noValidate className="space-y-4" onSubmit={(e) => { e.preventDefault(); save(); }}>
        <FormField label="Current password" required>
          <Input type="password" autoComplete="current-password" autoFocus value={current} onChange={(e) => setCurrent(e.target.value)} />
        </FormField>
        <FormField label="New password" required description={`At least ${MIN_PASSWORD} characters.`}>
          <Input type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
        </FormField>
        <FormField label="Confirm new password" required error={error}>
          <Input type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </FormField>
      </form>
    </Dialog>
  );
}

function EmailDialog({ onClose }: { onClose: () => void }) {
  const { showToast } = useToast();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const save = async () => {
    if (!email.includes('@')) return setError('Enter a valid email address.');
    setSaving(true);
    setError('');
    try {
      const res = await requestMyEmailChange(email.trim(), password);
      showToast(`Confirmation link sent to ${res.pending_email}`, 'success');
      onClose();
    } catch (err: any) {
      setError(err.message || 'Failed to change the email');
      setSaving(false);
    }
  };

  return (
    <Dialog
      title="Change email"
      description="We send a confirmation link to the new address. Your email changes when you open it."
      onClose={onClose}
      busy={saving}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={saving}>Cancel</Button>
          <Button variant="accent" onClick={save} loading={saving}>Send confirmation link</Button>
        </>
      }
    >
      <form noValidate className="space-y-4" onSubmit={(e) => { e.preventDefault(); save(); }}>
        <FormField label="New email address" required>
          <Input type="email" autoComplete="email" autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
        </FormField>
        <FormField label="Current password" required error={error}>
          <Input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </FormField>
      </form>
    </Dialog>
  );
}

/** Enrollment: password → scan and confirm a code → recovery codes. */
function SetupDialog({ username, onClose }: { username: string; onClose: () => void }) {
  const { showToast } = useToast();
  const [password, setPassword] = useState('');
  const [setup, setSetup] = useState<TOTPSetup | null>(null);
  const [code, setCode] = useState('');
  const [codes, setCodes] = useState<string[] | null>(null);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const start = async () => {
    setSaving(true);
    setError('');
    try {
      setSetup(await setupMyTOTP(password));
    } catch (err: any) {
      setError(err.message || 'Failed to start setup');
    } finally {
      setSaving(false);
    }
  };

  const confirm = async () => {
    setSaving(true);
    setError('');
    try {
      const res = await enableMyTOTP(code.trim());
      setCodes(res.recovery_codes);
      showToast('Two-factor authentication is on', 'success');
    } catch (err: any) {
      setError(err.message || 'Failed to confirm the code');
    } finally {
      setSaving(false);
    }
  };

  const footer = codes ? (
    <Button variant="accent" onClick={onClose}>I saved my recovery codes</Button>
  ) : setup ? (
    <>
      <Button variant="ghost" onClick={onClose} disabled={saving}>Cancel</Button>
      <Button variant="accent" onClick={confirm} loading={saving}>Turn on</Button>
    </>
  ) : (
    <>
      <Button variant="ghost" onClick={onClose} disabled={saving}>Cancel</Button>
      <Button variant="accent" onClick={start} loading={saving}>Continue</Button>
    </>
  );

  return (
    <Dialog
      title="Set up two-factor authentication"
      description={
        codes
          ? 'Two-factor authentication is on.'
          : setup
            ? 'Scan the code with an authenticator app, then enter the 6-digit code it shows.'
            : 'Confirm your password to start.'
      }
      onClose={onClose}
      busy={saving}
      dismissOnBackdrop={!codes}
      footer={footer}
    >
      {codes ? (
        <RecoveryCodes codes={codes} username={username} />
      ) : setup ? (
        <form noValidate className="space-y-4" onSubmit={(e) => { e.preventDefault(); confirm(); }}>
          <div className="flex flex-col items-center gap-3">
            <QrCode value={setup.otpauth_uri} label="QR code for your authenticator app" />
            <p className="text-center text-xs text-slate-500">
              Can&apos;t scan? Enter this key:{' '}
              <span className="select-all break-all font-mono text-slate-300">{setup.secret}</span>
            </p>
          </div>
          <FormField label="Code from the app" required error={error}>
            <Input
              inputMode="numeric"
              autoComplete="one-time-code"
              autoFocus
              maxLength={8}
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
          </FormField>
        </form>
      ) : (
        <form noValidate className="space-y-4" onSubmit={(e) => { e.preventDefault(); start(); }}>
          <FormField label="Current password" required error={error}>
            <Input type="password" autoComplete="current-password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} />
          </FormField>
        </form>
      )}
    </Dialog>
  );
}

/** Turning 2FA off or replacing recovery codes: password and a current code. */
function ProofDialog({ mode, username, onClose }: { mode: 'disable' | 'codes'; username: string; onClose: () => void }) {
  const { showToast } = useToast();
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [codes, setCodes] = useState<string[] | null>(null);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const disabling = mode === 'disable';

  const submit = async () => {
    setSaving(true);
    setError('');
    try {
      if (disabling) {
        await disableMyTOTP(password, code.trim());
        showToast('Two-factor authentication is off', 'success');
        onClose();
        return;
      }
      const res = await regenerateMyRecoveryCodes(password, code.trim());
      setCodes(res.recovery_codes);
    } catch (err: any) {
      setError(err.message || 'Failed');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog
      title={disabling ? 'Turn off two-factor authentication?' : 'New recovery codes'}
      description={
        codes
          ? 'Your old recovery codes no longer work.'
          : disabling
            ? 'Password sign-ins will no longer ask for a code.'
            : 'Replaces every recovery code you have.'
      }
      tone={disabling ? 'danger' : 'default'}
      onClose={onClose}
      busy={saving}
      dismissOnBackdrop={!codes}
      footer={
        codes ? (
          <Button variant="accent" onClick={onClose}>I saved my recovery codes</Button>
        ) : (
          <>
            <Button variant="ghost" onClick={onClose} disabled={saving}>Cancel</Button>
            <Button variant={disabling ? 'danger' : 'accent'} onClick={submit} loading={saving}>
              {disabling ? 'Turn off' : 'Replace codes'}
            </Button>
          </>
        )
      }
    >
      {codes ? (
        <RecoveryCodes codes={codes} username={username} />
      ) : (
        <form noValidate className="space-y-4" onSubmit={(e) => { e.preventDefault(); submit(); }}>
          <FormField label="Current password" required>
            <Input type="password" autoComplete="current-password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} />
          </FormField>
          <FormField label="Authenticator or recovery code" required error={error}>
            <Input autoComplete="one-time-code" value={code} onChange={(e) => setCode(e.target.value)} />
          </FormField>
        </form>
      )}
    </Dialog>
  );
}
