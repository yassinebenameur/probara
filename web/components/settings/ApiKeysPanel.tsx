'use client';

import { Ban, Copy, KeyRound, Plus } from 'lucide-react';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Pill from '@/components/ui/Pill';
import Callout from '@/components/ui/Callout';
import Input from '@/components/ui/Input';
import { ApiKeyScope } from '@/lib/types';
import Select from '@/components/ui/Select';
import { formatDateTime } from '@/lib/format';
import { RelativeTime } from '@/components/ui/RelativeTime';
import { isExpired, type ApiKeysState } from '@/components/settings/useApiKeys';

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

interface ApiKeysPanelProps {
  keys: ApiKeysState;
  /** Key create and revoke are tenant-admin gated server-side. */
  canManageKeys: boolean;
}

/** Settings → Access: the workspace's API keys, with create and revoke for admins. */
export function ApiKeysPanel({ keys, canManageKeys }: ApiKeysPanelProps) {
  const {
    apiKeys,
    loading,
    error,
    creating,
    newKeyName,
    setNewKeyName,
    newKeyScope,
    setNewKeyScope,
    newKeyExpiry,
    setNewKeyExpiry,
    createdKey,
    clipboard,
    setPendingRevoke,
    browserKeyPresent,
    showInactiveKeys,
    setShowInactiveKeys,
    loadKeys,
    handleCreate,
    activeKeys,
    inactiveCount,
  } = keys;
  const hasKeyControls = browserKeyPresent || canManageKeys || Boolean(createdKey?.key);

  return (
    <Panel
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
                <form onSubmit={handleCreate} className="flex flex-wrap items-end gap-3">
                  <div className="min-w-0 max-w-md grow basis-60">
                    <Input
                      label="API key name"
                      type="text"
                      value={newKeyName}
                      onChange={(event) => setNewKeyName(event.target.value)}
                      placeholder="e.g. Production agents"
                    />
                  </div>
                  <div className="w-full sm:w-40">
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
                  <div className="w-full sm:w-40">
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
                      onClick={() => void clipboard.copy(createdKey.key || '', 'new-key')}
                    >
                      {clipboard.copiedKey === 'new-key' ? 'Copied' : 'Copy'}
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
                <p className="mt-1 text-ui text-slate-400">
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
                        <p className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-slate-500">
                          <span>
                            ID <span className="font-mono">{key.id.slice(0, 8)}</span>
                          </span>
                          <span>
                            Fingerprint <span className="font-mono">{formatFingerprint(key.key_prefix)}</span>
                          </span>
                          <span>Created {formatDateTime(key.created_at)}</span>
                          {key.last_used_at && <RelativeTime value={key.last_used_at} prefix="Last used " />}
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
    </Panel>
  );
}
