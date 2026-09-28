'use client';

import { useEffect, useState } from 'react';
import { Ban, Copy, KeyRound, Plus } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import Callout from '@/components/ui/Callout';
import FilterChip from '@/components/ui/FilterChip';
import Input from '@/components/ui/Input';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { ApiKey, ApiKeyScope } from '@/lib/types';
import Select from '@/components/ui/Select';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { SSOPanel } from '@/components/settings/SSOPanel';
import { OidcGroupMappingsPanel } from '@/components/settings/OidcGroupMappingsPanel';
import { createApiKey, getApiKeys, getTenantSettings, revokeApiKey, updateTenantSettings } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import { formatDateTime } from '@/lib/format';
import { saveStoredApiKey, removeStoredApiKey } from '@/lib/api-keys';
import DashboardGroupsSection from '@/components/settings/DashboardGroupsSection';
import { NotificationsPanel } from '@/components/settings/NotificationsPanel';
import { AISettingsPanel } from '@/components/settings/AISettingsPanel';
import AlertChannelList from '@/components/alert-channels/AlertChannelList';
import Tabs, { useUrlTab } from '@/components/ui/Tabs';

function formatFingerprint(prefix: string | undefined): string {
  if (!prefix) return '-';
  return prefix.slice(0, 8);
}

const EXPIRY_WARN_DAYS = 14;

function expiryTone(expiresAt: string): 'danger' | 'warning' | 'neutral' {
  const remainingMs = new Date(expiresAt).getTime() - Date.now();
  if (remainingMs <= 0) return 'danger';
  if (remainingMs < EXPIRY_WARN_DAYS * 24 * 60 * 60 * 1000) return 'warning';
  return 'neutral';
}

function isExpired(expiresAt?: string | null): boolean {
  return Boolean(expiresAt) && new Date(expiresAt as string).getTime() <= Date.now();
}

function expiryLabel(expiresAt: string): string {
  const remainingMs = new Date(expiresAt).getTime() - Date.now();
  if (remainingMs <= 0) return 'Expired';
  const days = Math.ceil(remainingMs / (24 * 60 * 60 * 1000));
  return `Expires in ${days}d`;
}

export default function SettingsPage() {
  const { showToast } = useToast();
  const { isSuperadmin, role, actorType } = useCurrentUser();
  // Key create/revoke is tenant-admin gated server-side; mirror that here.
  const canManageKeys = actorType !== 'api_key' && (isSuperadmin || role === 'admin');
  const [apiKeys, setApiKeys] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [retentionLoading, setRetentionLoading] = useState(true);
  const [retentionError, setRetentionError] = useState('');
  const [savingRetention, setSavingRetention] = useState(false);
  const [retentionDays, setRetentionDays] = useState(0);
  const [retentionInput, setRetentionInput] = useState('');
  const [creating, setCreating] = useState(false);
  const [newKeyName, setNewKeyName] = useState('');
  const [newKeyScope, setNewKeyScope] = useState<ApiKeyScope>('write');
  const [newKeyExpiry, setNewKeyExpiry] = useState('');
  const [createdKey, setCreatedKey] = useState<ApiKey | null>(null);
  const [copiedField, setCopiedField] = useState('');
  const [pendingRevoke, setPendingRevoke] = useState<ApiKey | null>(null);
  const [revoking, setRevoking] = useState(false);
  const [browserKeyPresent, setBrowserKeyPresent] = useState(false);
  const [groupTags, setGroupTags] = useState<string[]>([]);
  const [showInactiveKeys, setShowInactiveKeys] = useState(false);
  const [tab, setTab] = useUrlTab(['general', 'channels', 'ai', 'access'] as const);

  useEffect(() => {
    setBrowserKeyPresent(Boolean(getApiKey()));
  }, []);

  const loadKeys = async () => {
    try {
      setLoading(true);
      setError('');
      const response = await getApiKeys({ page_size: 100 });
      setApiKeys(response.items || []);
    } catch (err: any) {
      setError(err.message || 'Failed to load API keys');
      setApiKeys([]);
    } finally {
      setLoading(false);
    }
  };

  const loadRetentionSettings = async () => {
    try {
      setRetentionLoading(true);
      setRetentionError('');
      const settings = await getTenantSettings();
      const value = settings.data_retention_days || 0;
      setRetentionDays(value);
      setRetentionInput(value === 0 ? '' : String(value));
      setGroupTags(settings.dashboard_group_tags || []);
    } catch (err: any) {
      setRetentionError(err.message || 'Failed to load data retention settings');
    } finally {
      setRetentionLoading(false);
    }
  };

  useEffect(() => {
    loadKeys();
    loadRetentionSettings();
  }, []);

  const handleSaveRetention = async () => {
    const trimmed = retentionInput.trim();
    const nextValue = trimmed === '' ? 0 : Number(trimmed);

    if (!Number.isInteger(nextValue)) {
      showToast('Retention must be an integer number of days', 'error');
      return;
    }
    if (nextValue !== 0 && (nextValue < 30 || nextValue > 3650)) {
      showToast('Retention must be 0 (Unlimited) or between 30 and 3650 days', 'error');
      return;
    }

    try {
      setSavingRetention(true);
      const updated = await updateTenantSettings({ data_retention_days: nextValue });
      const saved = updated.data_retention_days || 0;
      setRetentionDays(saved);
      setRetentionInput(saved === 0 ? '' : String(saved));
      showToast(saved === 0 ? 'Retention set to Unlimited' : `Retention set to ${saved} days`, 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to update retention', 'error');
    } finally {
      setSavingRetention(false);
    }
  };

  const copyToClipboard = (value: string, field: string) => {
    if (!value) return;
    navigator.clipboard.writeText(value);
    setCopiedField(field);
    setTimeout(() => setCopiedField(''), 2000);
  };

  const handleCreate = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!newKeyName.trim()) {
      showToast('API key name is required', 'error');
      return;
    }

    let expiresAt: string | undefined;
    if (newKeyExpiry) {
      const days = Number(newKeyExpiry);
      expiresAt = new Date(Date.now() + days * 24 * 60 * 60 * 1000).toISOString();
    }

    try {
      setCreating(true);
      const created = await createApiKey({
        name: newKeyName.trim(),
        scope: newKeyScope,
        expires_at: expiresAt,
      });
      setCreatedKey(created);
      setApiKeys((prev) => [created, ...prev]);
      if (created.key) {
        saveStoredApiKey({
          id: created.id,
          name: created.name,
          key: created.key,
          key_prefix: created.key_prefix,
          created_at: created.created_at,
        });
      }
      setNewKeyName('');
      showToast('API key created', 'success');
    } catch (err: any) {
      showToast(err.message || 'Failed to create API key', 'error');
    } finally {
      setCreating(false);
    }
  };

  const handleRevoke = async () => {
    if (!pendingRevoke) return;
    setRevoking(true);
    try {
      await revokeApiKey(pendingRevoke.id);
      setApiKeys((prev) =>
        prev.map((item) =>
          item.id === pendingRevoke.id ? { ...item, revoked_at: new Date().toISOString() } : item
        )
      );
      removeStoredApiKey(pendingRevoke.id);
      showToast('API key revoked', 'success');
      setPendingRevoke(null);
    } catch (err: any) {
      showToast(err.message || 'Failed to revoke API key', 'error');
    } finally {
      setRevoking(false);
    }
  };

  const activeKeys = apiKeys.filter((key) => !key.revoked_at && !isExpired(key.expires_at));
  const inactiveCount = apiKeys.length - activeKeys.length;
  const hasKeyControls = browserKeyPresent || canManageKeys || Boolean(createdKey?.key);

  const presets: Array<{ label: string; value: string }> = [
    { label: 'Unlimited', value: '' },
    { label: '30 days', value: '30' },
    { label: '90 days', value: '90' },
    { label: '365 days', value: '365' },
  ];

  return (
    <div className="space-y-6">
      <PageHeader title="Settings" />

      <Tabs
        tabs={[
          { id: 'general', label: 'General' },
          { id: 'channels', label: 'Channels' },
          { id: 'ai', label: 'AI' },
          { id: 'access', label: 'Access' },
        ]}
        active={tab}
        onChange={setTab}
      />

      {tab === 'general' && <NotificationsPanel />}

      {tab === 'channels' && <AlertChannelList />}

      {tab === 'ai' && <AISettingsPanel />}

      {tab === 'access' && isSuperadmin && <SSOPanel />}

      {tab === 'access' && isSuperadmin && <OidcGroupMappingsPanel />}

      {tab === 'general' && <Panel
        title="Data retention"
        subtitle="How long check result history is kept for this workspace."
      >
        {retentionLoading ? (
          <p className="text-sm text-slate-500">Loading retention settings…</p>
        ) : retentionError ? (
          <Callout
            tone="danger"
            action={<Button variant="ghost" size="xs" onClick={loadRetentionSettings}>Retry</Button>}
          >
            {retentionError}
          </Callout>
        ) : (
          <div className="space-y-6">
            <div>
              <div className="sm:w-60">
                <Input
                  label="Retention days"
                  type="number"
                  min={0}
                  max={3650}
                  value={retentionInput}
                  onChange={(event) => setRetentionInput(event.target.value)}
                  placeholder="Leave empty for Unlimited"
                />
              </div>
              <div className="mt-2 flex flex-wrap gap-1.5">
                {presets.map((preset) => (
                  <FilterChip
                    key={preset.label}
                    selected={retentionInput === preset.value}
                    onClick={() => setRetentionInput(preset.value)}
                  >
                    {preset.label}
                  </FilterChip>
                ))}
              </div>
              <p className="mt-2 max-w-2xl text-xs text-slate-500">
                Currently {retentionDays === 0 ? 'unlimited' : `${retentionDays} days`}. Empty keeps
                history forever; 30–3650 deletes older check data in the daily cleanup.
              </p>
            </div>
            <div className="flex justify-end">
              <Button
                variant="accent"
                size="sm"
                onClick={handleSaveRetention}
                disabled={savingRetention}
                loading={savingRetention}
              >
                {savingRetention ? 'Saving…' : 'Save retention'}
              </Button>
            </div>
          </div>
        )}
      </Panel>}

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
        />
      ))}

      {tab === 'access' && <Panel
        title="API keys"
        subtitle="Keys used by agents, scripts, and integrations."
      >
        {loading ? (
          <p className="text-sm text-slate-500">Loading API keys…</p>
        ) : error ? (
          <Callout
            tone="danger"
            action={<Button variant="ghost" size="xs" onClick={loadKeys}>Retry</Button>}
          >
            {error}
          </Callout>
        ) : (
          <>
            {hasKeyControls && (
              <div className="space-y-4">
                {browserKeyPresent && (
                  <p className="text-xs text-slate-400">This browser is signed in with a stored API key.</p>
                )}

                {canManageKeys && (
                  <form onSubmit={handleCreate} className="flex flex-col gap-3 lg:flex-row lg:items-end">
                    <div className="flex-1">
                      <Input
                        label="API key name"
                        type="text"
                        value={newKeyName}
                        onChange={(event) => setNewKeyName(event.target.value)}
                        placeholder="e.g. Production agents"
                      />
                    </div>
                    <div className="w-full lg:w-44">
                      <Select
                        label="Scope"
                        className="h-10"
                        value={newKeyScope}
                        onChange={(event) => setNewKeyScope(event.target.value as ApiKeyScope)}
                      >
                        <option value="write">Read &amp; write</option>
                        <option value="read">Read-only</option>
                      </Select>
                    </div>
                    <div className="w-full lg:w-44">
                      <Select
                        label="Expires"
                        className="h-10"
                        value={newKeyExpiry}
                        onChange={(event) => setNewKeyExpiry(event.target.value)}
                      >
                        <option value="">Never</option>
                        <option value="30">In 30 days</option>
                        <option value="90">In 90 days</option>
                        <option value="365">In 365 days</option>
                      </Select>
                    </div>
                    <Button
                      type="submit"
                      variant="accent"
                      size="sm"
                      icon={<Plus strokeWidth={1.75} />}
                      disabled={creating}
                      loading={creating}
                    >
                      {creating ? 'Creating…' : 'Create API key'}
                    </Button>
                  </form>
                )}

                {createdKey?.key && (
                  <Callout tone="success" title="New API key created">
                    Copy it now, this is the only time it will be shown.
                    <div className="mt-2 flex gap-2">
                      <code className="glass-well min-w-0 flex-1 overflow-x-auto px-3 py-2 font-mono text-xs text-slate-200">
                        {createdKey.key}
                      </code>
                      <Button
                        variant="ghost"
                        size="sm"
                        icon={<Copy strokeWidth={1.75} />}
                        onClick={() => copyToClipboard(createdKey.key || '', 'new-key')}
                      >
                        {copiedField === 'new-key' ? 'Copied' : 'Copy'}
                      </Button>
                    </div>
                  </Callout>
                )}
              </div>
            )}

            <div className={`-mx-5 -mb-5 ${hasKeyControls ? 'mt-5 border-t border-divider' : ''}`}>
              {apiKeys.length === 0 ? (
                <div className="flex flex-col items-center px-5 py-10 text-center">
                  <KeyRound className="h-5 w-5 text-slate-500" strokeWidth={1.5} aria-hidden="true" />
                  <p className="mt-3 text-sm font-medium text-white">No API keys yet</p>
                  <p className="mt-1 text-[13px] text-slate-400">
                    Create your first key above to grant agents and integrations access.
                  </p>
                </div>
              ) : (
                <>
                  <ul className="divide-y divide-divider">
                    {(showInactiveKeys ? apiKeys : activeKeys).map((key) => (
                      <li
                        key={key.id}
                        className="flex flex-col gap-3 px-5 py-3.5 transition-colors hover:bg-well sm:flex-row sm:items-center sm:justify-between"
                      >
                        <div className="min-w-0">
                          <p className="text-sm font-medium text-white">{key.name}</p>
                          <p className="mt-0.5 text-xs text-slate-500">
                            ID <span className="font-mono">{key.id.slice(0, 8)}</span> · Fingerprint{' '}
                            <span className="font-mono">{formatFingerprint(key.key_prefix)}</span> · Created{' '}
                            {formatDateTime(key.created_at, '-')}
                            {key.last_used_at && <> · Last used {formatDateTime(key.last_used_at, '-')}</>}
                          </p>
                          {key.revoked_at && (
                            <p className="mt-0.5 text-xs text-rose-300">
                              Revoked {formatDateTime(key.revoked_at, '-')}
                            </p>
                          )}
                        </div>
                        <div className="flex flex-shrink-0 items-center gap-2">
                          <Pill tone={key.scope === 'read' ? 'info' : 'neutral'} size="xs">
                            {key.scope === 'read' ? 'Read-only' : 'Read & write'}
                          </Pill>
                          {key.expires_at && !key.revoked_at && (
                            <Pill tone={expiryTone(key.expires_at)} size="xs">
                              {expiryLabel(key.expires_at)}
                            </Pill>
                          )}
                          {key.revoked_at && <Pill tone="danger" size="xs" dot>Revoked</Pill>}
                          {canManageKeys &&
                            (!key.revoked_at && !isExpired(key.expires_at) ? (
                              <IconButton
                                icon={<Ban strokeWidth={1.75} />}
                                label="Revoke"
                                onClick={() => setPendingRevoke(key)}
                                danger
                              />
                            ) : (
                              <span className="hidden h-8 w-8 shrink-0 sm:block" aria-hidden="true" />
                            ))}
                        </div>
                      </li>
                    ))}
                  </ul>
                  {inactiveCount > 0 && (
                    <div className="border-t border-divider px-3 py-2">
                      <Button variant="subtle" size="xs" onClick={() => setShowInactiveKeys((v) => !v)}>
                        {showInactiveKeys
                          ? 'Hide revoked and expired keys'
                          : `Show ${inactiveCount} revoked or expired key${inactiveCount === 1 ? '' : 's'}`}
                      </Button>
                    </div>
                  )}
                </>
              )}
            </div>
          </>
        )}
      </Panel>}

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
    </div>
  );
}
