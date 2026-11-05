'use client';

import { useState } from 'react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import Dialog from '@/components/ui/Dialog';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import CopyableTarget from '@/components/ui/CopyableTarget';
import SegmentedControl from '@/components/ui/SegmentedControl';
import { useToast } from '@/components/ui/ToastProvider';
import { resetUserMFA, sendUserInvite, sendUserPasswordReset } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import type { AccountLinkResponse, AdminUser } from '@/lib/types';

/**
 * Superadmin access actions for one user: send an invite (set-password link,
 * or a sign-in-with-SSO notice), send a password reset, and turn off a lost
 * second factor. Without email delivery the API returns the link instead,
 * shown here to copy and hand over: it is a credential until used.
 */
export default function UserAccessPanel({
  user,
  oidcEnabled,
  onChanged,
}: {
  user: AdminUser;
  oidcEnabled: boolean;
  onChanged: (patch: Partial<AdminUser>) => void;
}) {
  const { showToast } = useToast();
  const [inviteOpen, setInviteOpen] = useState(false);
  const [method, setMethod] = useState<'password' | 'sso'>(user.auth_method === 'oidc' ? 'sso' : 'password');
  const [busy, setBusy] = useState<'invite' | 'reset' | 'mfa' | null>(null);
  const [link, setLink] = useState<{ title: string; res: AccountLinkResponse } | null>(null);
  const [confirmMfa, setConfirmMfa] = useState(false);
  const linked = user.auth_method === 'oidc';

  const handled = (title: string, res: AccountLinkResponse, sentLabel: string) => {
    if (res.delivered) {
      showToast(`${sentLabel} sent to ${res.email}`, 'success');
    } else {
      setLink({ title, res });
    }
  };

  const invite = async () => {
    setBusy('invite');
    try {
      const res = await sendUserInvite(user.id, oidcEnabled ? method : 'password');
      setInviteOpen(false);
      handled('Invite link', res, 'Invite');
    } catch (err: any) {
      showToast(err.message || 'Failed to send the invite', 'error');
    } finally {
      setBusy(null);
    }
  };

  const reset = async () => {
    setBusy('reset');
    try {
      handled('Password reset link', await sendUserPasswordReset(user.id), 'Password reset');
    } catch (err: any) {
      showToast(err.message || 'Failed to create the reset link', 'error');
    } finally {
      setBusy(null);
    }
  };

  const resetMfa = async () => {
    setBusy('mfa');
    try {
      const res = await resetUserMFA(user.id);
      showToast(res.was_enabled ? 'Two-factor authentication turned off' : 'Two-factor was not on', 'success');
      onChanged({ mfa_enabled: false });
      setConfirmMfa(false);
    } catch (err: any) {
      showToast(err.message || 'Failed to reset two-factor', 'error');
    } finally {
      setBusy(null);
    }
  };

  const shown = link?.res.url || (link?.res.path && typeof window !== 'undefined' ? window.location.origin + link.res.path : '');

  return (
    <>
      <Panel title="Access">
        <div className="space-y-4">
          <div>
            <Button size="xs" onClick={() => setInviteOpen(true)}>Send invite</Button>
            <p className="mt-2 text-xs text-slate-500">
              {linked
                ? 'Tells them to sign in with single sign-on.'
                : 'A one-time link to set a password (valid 7 days), or a sign-in-with-SSO notice.'}
            </p>
          </div>
          {!linked && (
            <div>
              <Button size="xs" loading={busy === 'reset'} disabled={busy !== null} onClick={reset}>
                Send password reset
              </Button>
              <p className="mt-2 text-xs text-slate-500">A one-time reset link, valid one hour.</p>
            </div>
          )}
          <div>
            <p className="mb-2 flex items-center gap-2 text-ui text-slate-300">
              Two-factor
              <Pill tone={user.mfa_enabled ? 'success' : 'neutral'} size="xs">{user.mfa_enabled ? 'On' : 'Off'}</Pill>
            </p>
            {user.mfa_enabled && (
              <Button variant="danger" size="xs" onClick={() => setConfirmMfa(true)}>
                Reset two-factor
              </Button>
            )}
          </div>
        </div>
      </Panel>

      {inviteOpen && (
        <Dialog
          title={`Invite ${user.username}`}
          description={user.email ? `Sent to ${user.email} when email delivery is configured.` : 'This user has no email address: you get a link to hand over.'}
          onClose={() => setInviteOpen(false)}
          busy={busy === 'invite'}
          footer={
            <>
              <Button variant="ghost" onClick={() => setInviteOpen(false)} disabled={busy === 'invite'}>Cancel</Button>
              <Button variant="accent" onClick={invite} loading={busy === 'invite'}>Send invite</Button>
            </>
          }
        >
          {oidcEnabled && !linked ? (
            <SegmentedControl
              label="Sign-in method"
              options={[
                { value: 'password' as const, label: 'Set a password' },
                { value: 'sso' as const, label: 'Single sign-on' },
              ]}
              value={method}
              onChange={setMethod}
            />
          ) : null}
          <p className="text-ui text-slate-400">
            {linked || (oidcEnabled && method === 'sso')
              ? 'The email asks them to sign in with single sign-on; there is no password to set.'
              : 'The link lets them choose a password; it works once and replaces any password the account has.'}
          </p>
        </Dialog>
      )}

      {link && (
        <Dialog
          title={link.title}
          description={`Not emailed: ${link.res.reason || 'email delivery is not configured'}. Hand this link over yourself.`}
          onClose={() => setLink(null)}
          footer={<Button variant="accent" onClick={() => setLink(null)}>Done</Button>}
        >
          {shown ? (
            <div className="glass-well p-3">
              <CopyableTarget value={shown} label={link.title} href={null} textClassName="break-all text-slate-200" />
            </div>
          ) : null}
          {link.res.method === 'sso' ? (
            <p className="text-ui text-slate-400">Ask them to open it and sign in with single sign-on.</p>
          ) : (
            <Callout tone="warning">
              Anyone with this link can set this account&apos;s password
              {link.res.expires_at ? ` until ${formatDateTime(link.res.expires_at)}` : ''}. Share it privately.
            </Callout>
          )}
        </Dialog>
      )}

      <ConfirmDialog
        open={confirmMfa}
        title="Reset two-factor authentication?"
        description={`${user.username}'s authenticator and recovery codes stop working. They sign in with their password alone and can set two-factor up again.`}
        confirmLabel="Reset two-factor"
        tone="danger"
        loading={busy === 'mfa'}
        onConfirm={resetMfa}
        onCancel={() => setConfirmMfa(false)}
      />
    </>
  );
}
