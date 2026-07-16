'use client';

import { useEffect, useState } from 'react';
import { Copy, KeyRound, Plus, RefreshCw, X } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import { useToast } from '@/components/ui/ToastProvider';
import { ApiKey, ApiKeyScope } from '@/lib/types';
import Select from '@/components/ui/Select';
import { useCurrentUser } from '@/components/providers/CurrentUserProvider';
import { SSOPanel } from '@/components/settings/SSOPanel';
import { createApiKey, getApiKeys, getTenantSettings, revokeApiKey, updateTenantSettings } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import { formatDateTime } from '@/lib/format';
import { saveStoredApiKey, removeStoredApiKey } from '@/lib/api-keys';
import DashboardGroupsSection from '@/components/settings/DashboardGroupsSection';
import { NotificationsPanel } from '@/components/settings/NotificationsPanel';
import { AISettingsPanel } from '@/components/settings/AISettingsPanel';

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

  const presets: Array<{ label: string; value: string }> = [
    { label: 'Unlimited', value: '' },
    { label: '30 days', value: '30' },
    { label: '90 days', value: '90' },
    { label: '365 days', value: '365' },
  ];

  return (
    <div className="space-y-6">
      <PageHeader title="Settings" subtitle="Manage retention, API access, and integrations." />

      <NotificationsPanel />

      <AISettingsPanel />

      {isSuperadmin && <SSOPanel />}

      <Panel
        title="Data retention"
        subtitle="Control how long check result history is kept for this tenant."
        actions={(
          <Button
            variant="ghost"
            size="sm"
            icon={<RefreshCw strokeWidth={1.75} />}
            onClick={loadRetentionSettings}
          >
            Refresh
          </Button>
        )}
      >
        {retentionLoading ? (
          <div className="text-sm text-slate-500">Loading retention settings…</div>
        ) : retentionError ? (
          <div className="space-y-3">
            <p className="text-sm text-rose-400">{retentionError}</p>
            <Button variant="ghost" size="sm" onClick={loadRetentionSettings}>Retry</Button>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
              <p className="text-sm font-medium text-white">
                Current retention: {retentionDays === 0 ? 'Unlimited' : `${retentionDays} days`}
              </p>
              <p className="mt-1 text-xs text-slate-400">
                0 means unlimited history. Any value from 30 to 3650 deletes older check data during daily cleanup.
              </p>
            </div>

            <div className="space-y-2">
              <span className="block text-xs font-medium text-slate-400">Presets</span>
              <div className="flex flex-wrap gap-1.5">
                {presets.map((preset) => (
                  <Button
                    key={preset.label}
                    variant="ghost"
                    size="xs"
                    onClick={() => setRetentionInput(preset.value)}
                  >
                    {preset.label}
                  </Button>
                ))}
              </div>
            </div>

            <div className="flex flex-col gap-3 lg:flex-row lg:items-end">
              <div className="flex-1">
                <label className="mb-1.5 block text-xs font-medium text-slate-400">Retention days</label>
                <input
                  type="number"
                  min={0}
                  max={3650}
                  value={retentionInput}
                  onChange={(event) => setRetentionInput(event.target.value)}
                  placeholder="Leave empty for Unlimited"
                  className="input"
                />
                <p className="mt-1 text-xs text-slate-500">
                  Leave empty to store 0 (Unlimited), or enter a value between 30 and 3650.
                </p>
              </div>
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
      </Panel>

      <DashboardGroupsSection
        initialTags={groupTags}
        onSaved={(next) => setGroupTags(next)}
      />

      <Panel
        title="API keys"
        subtitle="Create and revoke keys used by agents, scripts, and integrations."
        actions={(
          <Button
            variant="ghost"
            size="sm"
            icon={<RefreshCw strokeWidth={1.75} />}
            onClick={loadKeys}
          >
            Refresh
          </Button>
        )}
      >
        {loading ? (
          <div className="text-sm text-slate-500">Loading API keys…</div>
        ) : error ? (
          <div className="space-y-3">
            <p className="text-sm text-rose-400">{error}</p>
            <Button variant="ghost" size="sm" onClick={loadKeys}>Retry</Button>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-white">Connected API key</p>
                  <p className="text-xs text-slate-400">
                    {browserKeyPresent
                      ? 'A key is stored in this browser for API key mode.'
                      : 'No key stored in this browser. Use /connect to add one.'}
                  </p>
                </div>
                <Pill tone={browserKeyPresent ? 'success' : 'neutral'} size="xs" dot>
                  {browserKeyPresent ? 'Connected' : 'Not set'}
                </Pill>
              </div>
            </div>

            {canManageKeys && (
            <form onSubmit={handleCreate} className="flex flex-col gap-3 lg:flex-row lg:items-end">
              <div className="flex-1">
                <label className="mb-1.5 block text-xs font-medium text-slate-400">API key name</label>
                <input
                  type="text"
                  value={newKeyName}
                  onChange={(event) => setNewKeyName(event.target.value)}
                  placeholder="e.g. Production agents"
                  className="input"
                />
              </div>
              <div className="w-full lg:w-44">
                <label className="mb-1.5 block text-xs font-medium text-slate-400">Scope</label>
                <Select
                  value={newKeyScope}
                  onChange={(event) => setNewKeyScope(event.target.value as ApiKeyScope)}
                >
                  <option value="write">Read &amp; write</option>
                  <option value="read">Read-only</option>
                </Select>
              </div>
              <div className="w-full lg:w-44">
                <label className="mb-1.5 block text-xs font-medium text-slate-400">Expires</label>
                <Select
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
              <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/10 px-4 py-3">
                <p className="text-xs text-emerald-200">
                  New API key created. Copy it now, this is the only time it will be shown.
                </p>
                <div className="mt-2 flex gap-2">
                  <code className="flex-1 overflow-x-auto rounded-lg border border-white/[0.06] bg-slate-900/60 px-3 py-2 font-mono text-xs text-emerald-200">
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
              </div>
            )}

            {apiKeys.length === 0 ? (
              <EmptyState
                icon={<KeyRound strokeWidth={1.5} />}
                title="No API keys yet"
                description="Create your first key above to grant agents and integrations access."
              />
            ) : (
              <div className="space-y-2">
                {apiKeys.map((key) => (
                  <div
                    key={key.id}
                    className="flex flex-col gap-3 rounded-lg border border-white/[0.06] bg-slate-900/40 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
                  >
                    <div>
                      <p className="text-sm font-medium text-white">{key.name}</p>
                      <p className="text-xs text-slate-400">
                        ID {key.id.slice(0, 8)} · Fingerprint {formatFingerprint(key.key_prefix)} · Created {formatDateTime(key.created_at, '-')}
                        {key.last_used_at && <> · Last used {formatDateTime(key.last_used_at, '-')}</>}
                      </p>
                      {key.revoked_at && (
                        <p className="text-xs text-rose-300">
                          Revoked {formatDateTime(key.revoked_at, '-')}
                        </p>
                      )}
                    </div>
                    <div className="flex items-center gap-2">
                      <Pill tone={key.scope === 'read' ? 'info' : 'neutral'} size="xs">
                        {key.scope === 'read' ? 'Read-only' : 'Read & write'}
                      </Pill>
                      {key.expires_at && !key.revoked_at && (
                        <Pill tone={expiryTone(key.expires_at)} size="xs">
                          {expiryLabel(key.expires_at)}
                        </Pill>
                      )}
                      <Pill tone={key.revoked_at ? 'danger' : 'success'} size="xs" dot>
                        {key.revoked_at ? 'Revoked' : 'Active'}
                      </Pill>
                      {!key.revoked_at && canManageKeys && (
                        <Button
                          variant="danger"
                          size="xs"
                          icon={<X strokeWidth={1.75} />}
                          onClick={() => setPendingRevoke(key)}
                        >
                          Revoke
                        </Button>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </Panel>

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
