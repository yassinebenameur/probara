'use client';

import { useCallback, useEffect, useState } from 'react';
import { Ban, Copy, KeyRound, Plus } from 'lucide-react';
import type { CreatedStatusPageAccessToken, StatusPage, StatusPageAccessToken } from '@/lib/types';
import {
  createStatusPageAccessToken,
  getStatusPageAccessTokens,
  revokeStatusPageAccessToken,
} from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import { resolveStatusPagePublicUrl } from '@/lib/statusPageUrl';
import { isGatedVisibility, kioskUrl } from '@/lib/status-page-access';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { useToast } from '@/components/ui/ToastProvider';
import Panel from '@/components/ui/Panel';
import Input from '@/components/ui/Input';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import Pill from '@/components/ui/Pill';

interface KioskTokensPanelProps {
  statusPage: StatusPage;
}

/**
 * Kiosk tokens open a password or private page without the password (wall
 * displays). Each token is shown once, at creation; revoking one signs out
 * every browser that opened the page with it.
 */
export default function KioskTokensPanel({ statusPage }: KioskTokensPanelProps) {
  const canWrite = useCanWrite();
  const { showToast } = useToast();
  const [tokens, setTokens] = useState<StatusPageAccessToken[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [label, setLabel] = useState('');
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<CreatedStatusPageAccessToken | null>(null);
  const [copied, setCopied] = useState(false);
  const [pendingRevoke, setPendingRevoke] = useState<StatusPageAccessToken | null>(null);
  const [revoking, setRevoking] = useState(false);
  const [showRevoked, setShowRevoked] = useState(false);

  const load = useCallback(async () => {
    try {
      setLoadError('');
      setTokens(await getStatusPageAccessTokens(statusPage.id));
    } catch (err: any) {
      setLoadError(err.message || 'Failed to load kiosk tokens');
    } finally {
      setLoading(false);
    }
  }, [statusPage.id]);

  useEffect(() => {
    load();
  }, [load]);

  const handleCreate = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!label.trim()) {
      showToast('Give the token a label', 'error');
      return;
    }
    try {
      setCreating(true);
      const token = await createStatusPageAccessToken(statusPage.id, label.trim());
      setCreated(token);
      setCopied(false);
      setLabel('');
      await load();
    } catch (err: any) {
      showToast(err.message || 'Failed to create kiosk token', 'error');
    } finally {
      setCreating(false);
    }
  };

  const handleRevoke = async () => {
    if (!pendingRevoke) return;
    try {
      setRevoking(true);
      await revokeStatusPageAccessToken(statusPage.id, pendingRevoke.id);
      if (created?.id === pendingRevoke.id) setCreated(null);
      setPendingRevoke(null);
      showToast('Kiosk token revoked', 'success');
      await load();
    } catch (err: any) {
      showToast(err.message || 'Failed to revoke kiosk token', 'error');
    } finally {
      setRevoking(false);
    }
  };

  const createdUrl = created
    ? kioskUrl(resolveStatusPagePublicUrl(statusPage), created.token, window.location.origin)
    : '';
  const copyCreated = () => {
    if (!createdUrl) return;
    navigator.clipboard.writeText(createdUrl);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  const active = tokens.filter((t) => !t.revoked_at);
  const revokedCount = tokens.length - active.length;
  const shown = showRevoked ? tokens : active;
  const gated = isGatedVisibility(statusPage.visibility ?? 'public');

  return (
    <Panel
      title="Kiosk tokens"
      subtitle={
        gated
          ? 'Open this page on a wall display or dashboard without the password. Each token is shown once.'
          : 'Kiosk tokens open password and private pages. This page is visible without one, so tokens do nothing until you change its visibility.'
      }
    >
      <div className="space-y-4">
        {canWrite && (
          <form onSubmit={handleCreate} className="flex flex-wrap items-end gap-3">
            <div className="min-w-0 max-w-md grow basis-60">
              <Input
                label="Token label"
                value={label}
                maxLength={100}
                onChange={(event) => setLabel(event.target.value)}
                placeholder="e.g. Lobby TV"
              />
            </div>
            <Button
              type="submit"
              variant="accent"
              size="sm"
              icon={<Plus strokeWidth={1.75} />}
              loading={creating}
              disabled={creating}
            >
              Create token
            </Button>
          </form>
        )}

        {created && (
          <Callout tone="success" title={`Kiosk token “${created.label}” created`}>
            Copy the kiosk URL now, this is the only time the token will be shown.
            <div className="mt-2 flex gap-2">
              <code className="glass-well min-w-0 flex-1 overflow-x-auto px-3 py-2 font-mono text-xs text-slate-200">
                {createdUrl}
              </code>
              <Button variant="ghost" size="sm" type="button" icon={<Copy strokeWidth={1.75} />} onClick={copyCreated}>
                {copied ? 'Copied' : 'Copy'}
              </Button>
            </div>
          </Callout>
        )}

        {loadError && <Callout tone="danger">{loadError}</Callout>}

        <div className="-mx-5 -mb-5 border-t border-divider">
          {loading ? (
            <p className="px-5 py-6 text-ui text-slate-500">Loading kiosk tokens…</p>
          ) : shown.length === 0 ? (
            <div className="flex flex-col items-center px-5 py-8 text-center">
              <KeyRound className="h-5 w-5 text-slate-500" strokeWidth={1.5} aria-hidden="true" />
              <p className="mt-3 text-sm font-medium text-white">No active kiosk tokens</p>
            </div>
          ) : (
            <ul className="divide-y divide-divider">
              {shown.map((token) => (
                <li
                  key={token.id}
                  className="flex flex-col gap-2 px-5 py-3 sm:flex-row sm:items-center sm:justify-between"
                >
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium text-white">{token.label}</p>
                    <p className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-slate-500">
                      <span>Created {formatDateTime(token.created_at)}</span>
                      {token.revoked_at && <span>Revoked {formatDateTime(token.revoked_at)}</span>}
                    </p>
                  </div>
                  <div className="flex flex-shrink-0 items-center gap-2">
                    {token.revoked_at ? (
                      <Pill tone="danger" size="xs" dot>
                        Revoked
                      </Pill>
                    ) : (
                      canWrite && (
                        <IconButton
                          icon={<Ban strokeWidth={1.75} />}
                          label="Revoke"
                          onClick={() => setPendingRevoke(token)}
                          danger
                        />
                      )
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
          {revokedCount > 0 && (
            <div className="border-t border-divider px-3 py-2">
              <Button variant="subtle" size="xs" type="button" onClick={() => setShowRevoked((v) => !v)}>
                {showRevoked
                  ? 'Hide revoked tokens'
                  : `Show ${revokedCount} revoked token${revokedCount === 1 ? '' : 's'}`}
              </Button>
            </div>
          )}
        </div>
      </div>

      <ConfirmDialog
        open={pendingRevoke !== null}
        title="Revoke kiosk token"
        description={
          pendingRevoke
            ? `Every display that opened this page with “${pendingRevoke.label}” loses access on its next refresh. This cannot be undone.`
            : undefined
        }
        confirmLabel="Revoke token"
        loading={revoking}
        onConfirm={handleRevoke}
        onCancel={() => !revoking && setPendingRevoke(null)}
      />
    </Panel>
  );
}
