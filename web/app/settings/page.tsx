'use client';

import Panel from '@/components/ui/Panel';
import PageHeader from '@/components/ui/PageHeader';
import Page from '@/components/ui/Page';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useCanWrite, useCurrentUser, useIsTenantAdmin } from '@/components/providers/CurrentUserProvider';
import { SSOPanel } from '@/components/settings/SSOPanel';
import { OidcGroupMappingsPanel } from '@/components/settings/OidcGroupMappingsPanel';
import DashboardGroupsSection from '@/components/settings/DashboardGroupsSection';
import { NotificationsPanel } from '@/components/settings/NotificationsPanel';
import { AISettingsPanel } from '@/components/settings/AISettingsPanel';
import { MCPPanel } from '@/components/settings/MCPPanel';
import { SETTINGS_COLUMNS, SETTINGS_COLUMNS_WIDE } from '@/components/settings/layout';
import AlertChannelList from '@/components/alert-channels/AlertChannelList';
import Tabs, { TabPanel, useUrlTab } from '@/components/ui/Tabs';
import { useApiKeys } from '@/components/settings/useApiKeys';
import { useRetentionSettings } from '@/components/settings/useRetentionSettings';
import { RetentionPanel } from '@/components/settings/RetentionPanel';
import { ApiKeysPanel } from '@/components/settings/ApiKeysPanel';

type SettingsTab = 'channels' | 'general' | 'ai' | 'mcp' | 'access';

// Per-tab layout on the full-width page. Notifications lays its section cards
// into column stacks itself (sectionLayout) above the full-width
// channel list, and AI is one wide panel that splits into columns inside, so
// both tabs stack their top-level blocks; General and MCP
// are two peer cards of similar height (stretched to match); Access puts the
// API keys and SSO status beside the group mappings once both forms fit.
// grid-cols-1 (minmax(0,1fr)), never a bare `grid`: an implicit auto track
// grows to its widest unbreakable row (channel table, picker rows) and pushes
// the panels past a phone viewport instead of letting them scroll inside.
//
// While Notifications' unsaved-changes bar floats over the bottom of the
// viewport (data-floating-bar), the tab grows room under its last panel so
// the end of the channel list can scroll clear of the bar. Padding at the
// end moves nothing above it.
const TAB_LAYOUT: Record<SettingsTab, string> = {
  channels: 'grid grid-cols-1 gap-6 [html:has([data-floating-bar])_&]:pb-24',
  general: `${SETTINGS_COLUMNS} items-stretch`,
  ai: 'grid grid-cols-1 gap-6',
  mcp: `${SETTINGS_COLUMNS} items-stretch`,
  access: `${SETTINGS_COLUMNS_WIDE} items-start`,
};

export default function SettingsPage() {
  // While identity loads, the permission hooks read false: hold the
  // read-only callouts and the permission-gated panels until it resolves, or
  // every admin sees a false "read-only" flash on each load.
  const { isSuperadmin, loading: userLoading } = useCurrentUser();
  // Key create/revoke and tenant settings (retention, dashboard groups) are
  // tenant-admin gated server-side; notification routing, channels and AI
  // only need write access. Mirror both here.
  const isTenantAdmin = useIsTenantAdmin();
  const canManageKeys = isTenantAdmin;
  const canWrite = useCanWrite();
  const keys = useApiKeys();
  const { pendingRevoke, setPendingRevoke, revoking, handleRevoke } = keys;
  const retention = useRetentionSettings();
  const { retentionLoading, groupTags, setGroupTags } = retention;
  // Notifications (default routing above the channel list) leads; its id stays
  // `channels` because /alert-channels and the routing badges link to it.
  const [tab, setTab] = useUrlTab<SettingsTab>(['channels', 'general', 'ai', 'mcp', 'access']);

  return (
    <Page width="data">
      <PageHeader
        title="Settings"
        subtitle="Notifications, data retention, AI, agent connections (MCP) and access for this workspace."
      />

      <Tabs
        tabs={[
          { id: 'channels', label: 'Notifications' },
          { id: 'general', label: 'General' },
          { id: 'ai', label: 'AI' },
          { id: 'mcp', label: 'MCP' },
          { id: 'access', label: 'Access' },
        ]}
        active={tab}
        onChange={setTab}
        idBase="settings"
      />

      <TabPanel idBase="settings" id={tab} className={TAB_LAYOUT[tab]}>
      {(tab === 'channels' || tab === 'ai') && !userLoading && !canWrite && (
        <Callout tone="info" className="col-span-full">You have read-only access to this workspace.</Callout>
      )}
      {tab === 'general' && !userLoading && !isTenantAdmin && (
        <Callout tone="info" className="col-span-full">
          Only workspace admins can change data retention and dashboard groups.
        </Callout>
      )}

      {tab === 'channels' && !userLoading && <NotificationsPanel readOnly={!canWrite} />}

      {tab === 'channels' && <AlertChannelList />}

      {tab === 'ai' && !userLoading && <AISettingsPanel readOnly={!canWrite} />}

      {tab === 'mcp' && <MCPPanel />}


      {tab === 'general' && <RetentionPanel retention={retention} isTenantAdmin={isTenantAdmin} />}

      {tab === 'general' && (retentionLoading ? (
        <Panel
          title="Dashboard groups"
          subtitle="Tags shown as service groups on the dashboard. Other tags stay available for filtering and search."
        >
          <p className="text-sm text-slate-500">Loading dashboard groups…</p>
        </Panel>
      ) : (
        <DashboardGroupsSection
          initialTags={groupTags}
          onSaved={(next) => setGroupTags(next)}
          readOnly={!isTenantAdmin}
        />
      ))}

      {/* Two column wrappers: API keys with the (short) SSO status on the left,
          the group mappings on the right, so the columns end at similar
          heights. Non-superadmins get the API keys column alone, full width. */}
      {tab === 'access' && <div className="min-w-0 space-y-6">
      <ApiKeysPanel keys={keys} canManageKeys={canManageKeys} />
      {isSuperadmin && <SSOPanel />}
      </div>}

      {tab === 'access' && isSuperadmin && (
        <div className="min-w-0">
          <OidcGroupMappingsPanel />
        </div>
      )}
      </TabPanel>

      <ConfirmDialog
        open={pendingRevoke !== null}
        title="Revoke API key"
        description={
          pendingRevoke
            ? `“${pendingRevoke.name}” will stop working immediately for any agent or integration using it. This cannot be undone.`
            : undefined
        }
        confirmLabel="Revoke key"
        loading={revoking}
        onConfirm={handleRevoke}
        onCancel={() => !revoking && setPendingRevoke(null)}
      />
    </Page>
  );
}
