'use client';

import { useEffect, useState } from 'react';
import { Copy, KeyRound, Plus, RefreshCw, X } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Toast from '@/components/ui/Toast';
import Button from '@/components/ui/Button';
import Pill from '@/components/ui/Pill';
import PageHeader from '@/components/ui/PageHeader';
import EmptyState from '@/components/ui/EmptyState';
import { ApiKey } from '@/lib/types';
import { createApiKey, getApiKeys, getTenantSettings, revokeApiKey, updateTenantSettings } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import { saveStoredApiKey, removeStoredApiKey } from '@/lib/api-keys';
import DashboardGroupsSection from '@/components/settings/DashboardGroupsSection';
import { NotificationsPanel } from '@/components/settings/NotificationsPanel';

type ToastState = { message: string; type: 'success' | 'error' } | null;

function formatDate(dateString?: string): string {
  if (!dateString) return '-';
  return new Date(dateString).toLocaleString();
}

function formatFingerprint(prefix: string | undefined): string {
  if (!prefix) return '-';
  return prefix.slice(0, 8);
}

export default function SettingsPage() {
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
  const [createdKey, setCreatedKey] = useState<ApiKey | null>(null);
  const [copiedField, setCopiedField] = useState('');
  const [toast, setToast] = useState<ToastState>(null);
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
      setToast({ message: 'Retention must be an integer number of days', type: 'error' });
      return;
    }
    if (nextValue !== 0 && (nextValue < 30 || nextValue > 3650)) {
      setToast({ message: 'Retention must be 0 (Unlimited) or between 30 and 3650 days', type: 'error' });
      return;
    }

    try {
      setSavingRetention(true);
      const updated = await updateTenantSettings({ data_retention_days: nextValue });
      const saved = updated.data_retention_days || 0;
      setRetentionDays(saved);
      setRetentionInput(saved === 0 ? '' : String(saved));
      setToast({ message: saved === 0 ? 'Retention set to Unlimited' : `Retention set to ${saved} days`, type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to update retention', type: 'error' });
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
      setToast({ message: 'API key name is required', type: 'error' });
      return;
    }

    try {
      setCreating(true);
      const created = await createApiKey({ name: newKeyName.trim() });
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
      setToast({ message: 'API key created', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to create API key', type: 'error' });
    } finally {
      setCreating(false);
    }
  };

  const handleRevoke = async (key: ApiKey) => {
    if (!confirm(`Revoke API key "${key.name}"? This cannot be undone.`)) {
      return;
    }

    try {
      await revokeApiKey(key.id);
      setApiKeys((prev) =>
        prev.map((item) =>
          item.id === key.id ? { ...item, revoked_at: new Date().toISOString() } : item
        )
      );
      removeStoredApiKey(key.id);
      setToast({ message: 'API key revoked', type: 'success' });
    } catch (err: any) {
      setToast({ message: err.message || 'Failed to revoke API key', type: 'error' });
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
                        ID {key.id.slice(0, 8)} · Fingerprint {formatFingerprint(key.key_prefix)} · Created {formatDate(key.created_at)}
                      </p>
                      {key.revoked_at && (
                        <p className="text-xs text-rose-300">
                          Revoked {formatDate(key.revoked_at)}
                        </p>
                      )}
                    </div>
                    <div className="flex items-center gap-2">
                      <Pill tone={key.revoked_at ? 'danger' : 'success'} size="xs" dot>
                        {key.revoked_at ? 'Revoked' : 'Active'}
                      </Pill>
                      {!key.revoked_at && (
                        <Button
                          variant="danger"
                          size="xs"
                          icon={<X strokeWidth={1.75} />}
                          onClick={() => handleRevoke(key)}
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

      {toast && (
        <Toast message={toast.message} type={toast.type} onClose={() => setToast(null)} />
      )}
    </div>
  );
}
