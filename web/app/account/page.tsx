'use client';

import Page from '@/components/ui/Page';
import PageHeader from '@/components/ui/PageHeader';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Tabs, { TabPanel, useUrlTab } from '@/components/ui/Tabs';
import ProfilePanel from '@/components/account/ProfilePanel';
import SessionsPanel from '@/components/account/SessionsPanel';
import PasswordPanel from '@/components/account/PasswordPanel';
import EmailPanel from '@/components/account/EmailPanel';
import TwoFactorPanel from '@/components/account/TwoFactorPanel';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { getAccountSecurity, getMySessions, revokeMyOtherSessions, revokeMySession } from '@/lib/api';
import { useAsyncData } from '@/lib/useAsyncData';
import type { AccountSecurity } from '@/lib/types';

type AccountTab = 'profile' | 'password' | 'email' | 'two-factor' | 'sessions';

const TABS = ['profile', 'password', 'email', 'two-factor', 'sessions'] as const;

/**
 * The signed-in user's own account. Users are platform-scoped, so nothing
 * here depends on the selected workspace.
 */
export default function AccountPage() {
  const { user, actorType, loading } = useCurrentUser();
  const [tab, setTab] = useUrlTab<AccountTab>(TABS);

  return (
    <Page width="reading">
      <PageHeader title="Account" subtitle="Your profile, sign-in security and the browsers signed in to your account." />

      {loading ? (
        <p className="text-sm text-slate-500">Loading account…</p>
      ) : actorType === 'api_key' || !user ? (
        <Callout tone="info">Account settings need a signed-in user session; an API key has no account.</Callout>
      ) : (
        <>
          <Tabs
            tabs={[
              { id: 'profile', label: 'Profile' },
              { id: 'password', label: 'Password' },
              { id: 'email', label: 'Email' },
              { id: 'two-factor', label: 'Two-factor' },
              { id: 'sessions', label: 'Sessions' },
            ]}
            active={tab}
            onChange={setTab}
            idBase="account"
          />
          <TabPanel idBase="account" id={tab} className="space-y-6">
            {tab === 'profile' && <ProfilePanel user={user} />}
            {(tab === 'password' || tab === 'email' || tab === 'two-factor') && (
              <SecurityTab tab={tab} username={user.username} />
            )}
            {tab === 'sessions' && (
              <SessionsPanel
                scope="self"
                load={getMySessions}
                revoke={revokeMySession}
                revokeAll={revokeMyOtherSessions}
              />
            )}
          </TabPanel>
        </>
      )}
    </Page>
  );
}

const EMPTY_SECURITY: AccountSecurity = {
  auth_method: 'password',
  local: false,
  has_password: false,
  email_delivery: false,
  two_factor: { enabled: false, recovery_codes_remaining: 0, enrolment_available: false },
};

/** The Password, Email and Two-factor tabs, over one load of the account's sign-in state. */
function SecurityTab({ tab, username }: { tab: 'password' | 'email' | 'two-factor'; username: string }) {
  const { data, loading, error, reload } = useAsyncData<AccountSecurity>(
    getAccountSecurity,
    EMPTY_SECURITY,
    'Failed to load your sign-in settings'
  );
  if (loading) return <p className="text-sm text-slate-500">Loading…</p>;
  if (error) {
    return (
      <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={reload}>Retry</Button>}>
        {error}
      </Callout>
    );
  }
  if (tab === 'password') return <PasswordPanel security={data} />;
  if (tab === 'email') return <EmailPanel security={data} onChanged={reload} />;
  return <TwoFactorPanel security={data} username={username} onChanged={reload} />;
}
