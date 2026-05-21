'use client';

import { useEffect, useState } from 'react';
import Panel from '@/components/ui/Panel';
import Toast from '@/components/ui/Toast';
import { ApiKey } from '@/lib/types';
import { createApiKey, getApiKeys, getTenantSettings, revokeApiKey, updateTenantSettings } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import { saveStoredApiKey, removeStoredApiKey } from '@/lib/api-keys';
import DashboardGroupsSection from '@/components/settings/DashboardGroupsSection';

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

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-semibold text-white">Settings</h1>
        <p className="text-sm text-muted">Manage retention, API access, and integrations.</p>
      </div>

      <Panel
        title="Data Retention"
        subtitle="Control how long check result history is kept for this tenant."
        actions={(
          <button
            onClick={loadRetentionSettings}
            className="btn btn-secondary btn-sm"
          >
            Refresh
          </button>
        )}
      >
        {retentionLoading ? (
          <div className="text-sm text-muted">Loading retention settings...</div>
        ) : retentionError ? (
          <div className="space-y-3">
            <p className="text-sm text-rose-400">{retentionError}</p>
            <button
              onClick={loadRetentionSettings}
              className="btn btn-danger btn-sm"
            >
              Retry
            </button>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="rounded-lg border border-white/[0.08] bg-slate-800/30 px-4 py-3">
              <p className="text-sm font-medium text-white">
                Current retention: {retentionDays === 0 ? 'Unlimited' : `${retentionDays} days`}
              </p>
              <p className="text-xs text-muted mt-1">
                0 means unlimited history. Any value from 30 to 3650 deletes older check data during daily cleanup.
              </p>
            </div>

            <div className="space-y-2">
              <span className="block text-xs font-medium text-slate-400">Presets</span>
              <div className="flex flex-wrap gap-2">
                <button
                  type="button"
                  onClick={() => setRetentionInput('')}
                  className="btn btn-secondary btn-sm"
                >
                  Unlimited
                </button>
                <button
                  type="button"
                  onClick={() => setRetentionInput('30')}
                  className="btn btn-secondary btn-sm"
                >
                  30 days
                </button>
                <button
                  type="button"
                  onClick={() => setRetentionInput('90')}
                  className="btn btn-secondary btn-sm"
                >
                  90 days
                </button>
                <button
                  type="button"
                  onClick={() => setRetentionInput('365')}
                  className="btn btn-secondary btn-sm"
                >
                  365 days
                </button>
              </div>
            </div>

            <div className="flex flex-col gap-3 lg:flex-row lg:items-end">
              <div className="flex-1">
                <label className="block text-xs font-medium text-slate-400 mb-1.5">Retention Days</label>
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
              <button
                type="button"
                onClick={handleSaveRetention}
                disabled={savingRetention}
                className="btn btn-primary btn-sm disabled:opacity-50"
              >
                {savingRetention ? 'Saving...' : 'Save Retention'}
              </button>
            </div>
          </div>
        )}
      </Panel>

      <DashboardGroupsSection
        initialTags={groupTags}
        onSaved={(next) => setGroupTags(next)}
      />

      <Panel
        title="API Keys"
        subtitle="Create and revoke keys used by agents, scripts, and integrations."
        actions={(
          <button
            onClick={loadKeys}
            className="btn btn-secondary btn-sm"
          >
            Refresh
          </button>
        )}
      >
        {loading ? (
          <div className="text-sm text-muted">Loading API keys...</div>
        ) : error ? (
          <div className="space-y-3">
            <p className="text-sm text-rose-400">{error}</p>
            <button
              onClick={loadKeys}
              className="btn btn-danger btn-sm"
            >
              Retry
            </button>
          </div>
        ) : (
          <div className="space-y-4">
            <div className="rounded-lg border border-white/[0.08] bg-slate-800/30 px-4 py-3">
              <div className="flex flex-wrap items-center justify-between gap-3">
                <div>
                  <p className="text-sm font-medium text-white">Connected API Key</p>
                  <p className="text-xs text-muted">
                    {browserKeyPresent
                      ? 'A key is stored in this browser for API key mode.'
                      : 'No key stored in this browser. Use /connect to add one.'}
                  </p>
                </div>
                <span
                  className={browserKeyPresent ? 'badge badge-success' : 'badge badge-default'}
                >
                  {browserKeyPresent ? 'Connected' : 'Not set'}
                </span>
              </div>
            </div>

            <form onSubmit={handleCreate} className="flex flex-col gap-3 lg:flex-row lg:items-end">
              <div className="flex-1">
                <label className="block text-xs font-medium text-slate-400 mb-1.5">API Key Name</label>
                <input
                  type="text"
                  value={newKeyName}
                  onChange={(event) => setNewKeyName(event.target.value)}
                  placeholder="e.g. Production agents"
                  className="input"
                />
              </div>
              <button
                type="submit"
                disabled={creating}
                className="btn btn-primary btn-sm disabled:opacity-50"
              >
                {creating ? 'Creating...' : 'Create API Key'}
              </button>
            </form>

            {createdKey?.key && (
              <div className="rounded-lg border border-emerald-500/20 bg-emerald-500/10 px-4 py-3">
                <p className="text-xs text-emerald-200">
                  New API key created. Copy it now, this is the only time it will be shown.
                </p>
                <div className="mt-2 flex gap-2">
                  <code className="flex-1 rounded-lg border border-white/[0.08] bg-slate-800/50 px-3 py-2 text-xs text-emerald-200 font-mono overflow-x-auto">
                    {createdKey.key}
                  </code>
                  <button
                    type="button"
                    onClick={() => copyToClipboard(createdKey.key || '', 'new-key')}
                    className="btn btn-secondary btn-sm"
                  >
                    {copiedField === 'new-key' ? 'Copied' : 'Copy'}
                  </button>
                </div>
              </div>
            )}

            <div className="space-y-2">
              {apiKeys.length === 0 ? (
                <p className="text-sm text-muted">No API keys created yet.</p>
              ) : (
                apiKeys.map((key) => (
                  <div
                    key={key.id}
                    className="flex flex-col gap-3 rounded-lg border border-white/[0.08] bg-slate-800/30 px-4 py-3 sm:flex-row sm:items-center sm:justify-between"
                  >
                    <div>
                      <p className="text-sm font-medium text-white">{key.name}</p>
                      <p className="text-xs text-muted">
                        ID {key.id.slice(0, 8)} | Fingerprint {formatFingerprint(key.key_prefix)} | Created {formatDate(key.created_at)}
                      </p>
                      {key.revoked_at && (
                        <p className="text-xs text-rose-300">
                          Revoked {formatDate(key.revoked_at)}
                        </p>
                      )}
                    </div>
                    <div className="flex items-center gap-2">
                      <span
                        className={key.revoked_at ? 'badge badge-danger' : 'badge badge-success'}
                      >
                        {key.revoked_at ? 'Revoked' : 'Active'}
                      </span>
                      {!key.revoked_at && (
                        <button
                          type="button"
                          onClick={() => handleRevoke(key)}
                          className="btn btn-danger btn-sm"
                        >
                          Revoke
                        </button>
                      )}
                    </div>
                  </div>
                ))
              )}
            </div>
          </div>
        )}
      </Panel>

      {toast && (
        <Toast message={toast.message} type={toast.type} onClose={() => setToast(null)} />
      )}
    </div>
  );
}
