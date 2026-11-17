'use client';

import { useEffect, useState } from 'react';
import { KeyRound, Mail, ShieldOff } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import CopyableTarget from '@/components/ui/CopyableTarget';
import { useToast } from '@/components/ui/ToastProvider';
import { createPasswordResetLink, getOidcStatus, inviteUser, resetUserTwoFactor } from '@/lib/api';
import { absoluteLink } from '@/lib/account-links';
import { errorMessage } from '@/lib/errors';
import { formatDateTime } from '@/lib/format';
import type { AccountLinkResponse, AdminUser } from '@/lib/types';

/**
 * A superadmin's account actions for another user: send an invite (password
 * or single sign-on), generate a password reset link, reset two-factor
 * sign-in after a lost device. With email delivery a link is mailed to the
 * user; without it the link is shown here once, to hand over.
 */
export default function UserAccountActions({ user, onChanged }: { user: AdminUser; onChanged: () => void }) {
  const { showToast } = useToast();
  const [ssoEnabled, setSsoEnabled] = useState(false);
  const [busy, setBusy] = useState<'' | 'invite' | 'sso' | 'reset' | 'two-factor'>('');
  const [result, setResult] = useState<{ title: string; response: AccountLinkResponse } | null>(null);
  const [confirmReset, setConfirmReset] = useState(false);

  useEffect(() => {
    let ignore = false;
    getOidcStatus().then((status) => {
      if (!ignore) setSsoEnabled(status.enabled);
    });
    return () => {
      ignore = true;
    };
  }, []);

  const linked = user.auth_method === 'oidc';
  const hasPassword = user.has_password === true;
  const disabled = Boolean(user.disabled_at);

  const issue = async (kind: 'invite' | 'sso' | 'reset') => {
    setBusy(kind);
    setResult(null);
    try {
      const response =
        kind === 'reset'
          ? await createPasswordResetLink(user.id)
          : await inviteUser(user.id, kind === 'sso' ? 'sso' : 'password');
      const title = kind === 'reset' ? 'Password reset link' : kind === 'sso' ? 'Single sign-on invite' : 'Invite';
      if (response.delivered) {
        showToast(`${title} emailed to ${response.email ?? user.username}`, 'success');
      } else {
        setResult({ title, response });
      }
    } catch (err) {
      showToast(errorMessage(err, 'The request failed'), 'error');
    } finally {
      setBusy('');
    }
  };

  const resetTwoFactor = async () => {
    setBusy('two-factor');
    try {
      const { was_enabled } = await resetUserTwoFactor(user.id);
      showToast(was_enabled ? 'Two-factor sign-in reset' : 'Two-factor sign-in was not on', 'success');
      setConfirmReset(false);
      onChanged();
    } catch (err) {
      showToast(errorMessage(err, 'Resetting two-factor sign-in failed'), 'error');
    } finally {
      setBusy('');
    }
  };

  const link = result?.response.link ? absoluteLink(result.response.link, window.location.origin) : '';

  return (
    <Panel
      title="Sign-in access"
      subtitle="Invites and reset links are single use; a new one replaces the previous one. They are emailed when this installation sends email, otherwise shown here once to hand over."
    >
      <dl className="mb-4 grid gap-4 sm:grid-cols-2">
        <div>
          <dt className="text-xs text-slate-500">Password</dt>
          <dd className="mt-0.5 text-ui text-slate-300">
            {linked ? 'Managed by the identity provider' : hasPassword ? 'Set' : 'Not set yet'}
          </dd>
        </div>
        <div>
          <dt className="text-xs text-slate-500">Two-factor sign-in</dt>
          <dd className="mt-0.5 text-ui text-slate-300">{user.two_factor_enabled ? 'On' : 'Off'}</dd>
        </div>
      </dl>

      {disabled ? (
        <Callout tone="info">The account is disabled: no invite or reset link can be issued.</Callout>
      ) : (
        <div className="flex flex-wrap gap-2">
          {!linked && !hasPassword && (
            <Button size="sm" icon={<Mail />} loading={busy === 'invite'} disabled={busy !== ''} onClick={() => issue('invite')}>
              Send invite
            </Button>
          )}
          {ssoEnabled && !hasPassword && (
            <Button size="sm" icon={<Mail />} loading={busy === 'sso'} disabled={busy !== ''} onClick={() => issue('sso')}>
              Send single sign-on invite
            </Button>
          )}
          {!linked && hasPassword && (
            <Button size="sm" icon={<KeyRound />} loading={busy === 'reset'} disabled={busy !== ''} onClick={() => issue('reset')}>
              Generate reset link
            </Button>
          )}
          {user.two_factor_enabled && (
            <Button
              variant="danger"
              size="sm"
              icon={<ShieldOff />}
              disabled={busy !== ''}
              onClick={() => setConfirmReset(true)}
            >
              Reset two-factor
            </Button>
          )}
        </div>
      )}
      {linked && !disabled && (
        <p className="mt-3 text-xs text-slate-500">
          This account signs in with single sign-on: its password, email address and second factor are managed by the
          identity provider.
        </p>
      )}

      {result && (
        <div className="mt-4 space-y-3">
          <Callout tone="warning" title={`${result.title} not emailed`}>
            {result.response.reason ? `${result.response.reason[0].toUpperCase()}${result.response.reason.slice(1)}. ` : ''}
            {result.response.method === 'sso'
              ? 'Tell the user to sign in with single sign-on on the sign-in page.'
              : 'Copy the link and send it to the user over a channel you trust. It is shown only now.'}
          </Callout>
          {link && (
            <div className="glass-well rounded-xl p-3">
              <CopyableTarget value={link} label="link" href={null} />
              {result.response.expires_at && (
                <p className="mt-1 text-xs text-slate-500">Works once; expires {formatDateTime(result.response.expires_at)}.</p>
              )}
            </div>
          )}
        </div>
      )}

      <ConfirmDialog
        open={confirmReset}
        title="Reset two-factor sign-in?"
        description={`${user.username} signs in with the password alone until they set up two-factor sign-in again. Their recovery codes stop working and a pending email change is cancelled. Their sessions stay signed in.`}
        confirmLabel="Reset two-factor"
        tone="danger"
        loading={busy === 'two-factor'}
        onConfirm={resetTwoFactor}
        onCancel={() => setConfirmReset(false)}
      />
    </Panel>
  );
}
