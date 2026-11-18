'use client';

import Page from '@/components/ui/Page';
import PageHeader from '@/components/ui/PageHeader';
import Callout from '@/components/ui/Callout';
import Tabs, { TabPanel, useUrlTab } from '@/components/ui/Tabs';
import ProfilePanel from '@/components/account/ProfilePanel';
import SessionsPanel from '@/components/account/SessionsPanel';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { getMySessions, revokeMyOtherSessions, revokeMySession } from '@/lib/api';

type AccountTab = 'profile' | 'sessions';

/**
 * The signed-in user's own account. Users are platform-scoped, so nothing
 * here depends on the selected workspace.
 */
export default function AccountPage() {
  const { user, actorType, loading } = useCurrentUser();
  const [tab, setTab] = useUrlTab<AccountTab>(['profile', 'sessions']);

  return (
    <Page width="reading">
      <PageHeader title="Account" subtitle="Your profile and the browsers signed in to your account." />

      {loading ? (
        <p className="text-sm text-slate-500">Loading account…</p>
      ) : actorType === 'api_key' || !user ? (
        <Callout tone="info">Account settings need a signed-in user session; an API key has no account.</Callout>
      ) : (
        <>
          <Tabs
            tabs={[
              { id: 'profile', label: 'Profile' },
              { id: 'sessions', label: 'Sessions' },
            ]}
            active={tab}
            onChange={setTab}
            idBase="account"
          />
          <TabPanel idBase="account" id={tab} className="space-y-6">
            {tab === 'profile' && <ProfilePanel user={user} />}
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
