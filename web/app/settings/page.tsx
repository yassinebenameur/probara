'use client';

import { useEffect, useState } from 'react';
import Panel from '@/components/ui/Panel';
import Toast from '@/components/ui/Toast';
import { ApiKey } from '@/lib/types';
import { createApiKey, getApiKeys, revokeApiKey } from '@/lib/api';
import { getApiKey } from '@/lib/auth';
import { saveStoredApiKey, removeStoredApiKey } from '@/lib/api-keys';

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
  const [creating, setCreating] = useState(false);
  const [newKeyName, setNewKeyName] = useState('');
  const [createdKey, setCreatedKey] = useState<ApiKey | null>(null);
  const [copiedField, setCopiedField] = useState('');
  const [toast, setToast] = useState<ToastState>(null);
  const [browserKeyPresent, setBrowserKeyPresent] = useState(false);

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

  useEffect(() => {
    loadKeys();
  }, []);

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
        <p className="text-sm text-muted">Manage API access for agents and integrations.</p>
      </div>

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
