'use client';

import { useCallback, useEffect, useState } from 'react';
import { Check, Copy, Globe, Minus, Plus, RefreshCw, Trash2, X } from 'lucide-react';
import type { StatusPage, StatusPageDomain, StatusPageDomainRecord } from '@/lib/types';
import {
  addStatusPageDomain,
  getStatusPageDomains,
  removeStatusPageDomain,
  verifyStatusPageDomain,
} from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import { errorMessage } from '@/lib/errors';
import { useClipboard } from '@/lib/useClipboard';
import {
  cleanHostnameInput,
  STATUS_PAGE_DOMAIN_LABEL,
  statusPageDomainHint,
  statusPageDomainState,
} from '@/lib/status-page-domains';
import { statusPageDomainTone } from '@/lib/status-tones';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { useToast } from '@/components/ui/ToastProvider';
import Panel from '@/components/ui/Panel';
import Input from '@/components/ui/Input';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import Pill from '@/components/ui/Pill';
import { RelativeTime } from '@/components/ui/RelativeTime';

interface DomainsPanelProps {
  statusPage: StatusPage;
  /** Called after a change that can move the page's canonical URL. */
  onChanged?: () => void;
}

/**
 * A status page's custom domains: the DNS records each one needs, what the
 * last check found, and verify-now. Only verified domains are served; the
 * oldest verified one is the page's canonical URL.
 */
export default function DomainsPanel({ statusPage, onChanged }: DomainsPanelProps) {
  const canWrite = useCanWrite();
  const { showToast } = useToast();
  const [domains, setDomains] = useState<StatusPageDomain[]>([]);
  const [enabled, setEnabled] = useState(true);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [hostname, setHostname] = useState('');
  const [adding, setAdding] = useState(false);
  const [verifying, setVerifying] = useState<string | null>(null);
  const [pendingRemove, setPendingRemove] = useState<StatusPageDomain | null>(null);
  const [removing, setRemoving] = useState(false);

  const load = useCallback(async () => {
    try {
      setLoadError('');
      const list = await getStatusPageDomains(statusPage.id);
      setDomains(list.items);
      setEnabled(list.enabled);
    } catch (err) {
      setLoadError(errorMessage(err, 'Failed to load custom domains'));
    } finally {
      setLoading(false);
    }
  }, [statusPage.id]);

  useEffect(() => {
    load();
  }, [load]);

  const handleAdd = async (event: React.FormEvent) => {
    event.preventDefault();
    const host = cleanHostnameInput(hostname);
    if (!host) {
      showToast('Enter a hostname such as status.example.com', 'error');
      return;
    }
    try {
      setAdding(true);
      await addStatusPageDomain(statusPage.id, host);
      setHostname('');
      await load();
    } catch (err) {
      showToast(errorMessage(err, 'Failed to add the domain'), 'error');
    } finally {
      setAdding(false);
    }
  };

  const handleVerify = async (domain: StatusPageDomain) => {
    try {
      setVerifying(domain.id);
      const checked = await verifyStatusPageDomain(domain.id);
      setDomains((current) => current.map((d) => (d.id === checked.id ? checked : d)));
      if (checked.status !== domain.status || checked.primary !== domain.primary) onChanged?.();
      showToast(
        checked.status === 'verified' && !checked.failing_since
          ? `${checked.hostname} is verified`
          : `${checked.hostname}: ${checked.last_error || 'not verified yet'}`,
        checked.status === 'verified' && !checked.failing_since ? 'success' : 'error'
      );
    } catch (err) {
      showToast(errorMessage(err, 'Failed to verify the domain'), 'error');
    } finally {
      setVerifying(null);
    }
  };

  const handleRemove = async () => {
    if (!pendingRemove) return;
    try {
      setRemoving(true);
      await removeStatusPageDomain(pendingRemove.id);
      setPendingRemove(null);
      showToast('Custom domain removed', 'success');
      await load();
      onChanged?.();
    } catch (err) {
      showToast(errorMessage(err, 'Failed to remove the domain'), 'error');
    } finally {
      setRemoving(false);
    }
  };

  return (
    <Panel
      title="Custom domains"
      subtitle="Serve this page on your own hostname, such as status.example.com. Each domain is served once its two DNS records check out; the oldest verified one becomes the page's canonical address."
    >
      <div className="space-y-4">
        {!loading && !enabled && (
          <Callout tone="warning" title="Custom domains are off on this installation">
            An administrator turns them on by setting STATUS_PAGE_CNAME_TARGET on the API (Helm: statusPage.cnameTarget
            or statusPage.baseUrl).
          </Callout>
        )}

        {canWrite && enabled && (
          <form onSubmit={handleAdd} className="flex flex-wrap items-end gap-3">
            <div className="min-w-0 max-w-md grow basis-60">
              <Input
                label="Hostname"
                value={hostname}
                maxLength={253}
                autoComplete="off"
                spellCheck={false}
                onChange={(event) => setHostname(event.target.value)}
                placeholder="status.example.com"
              />
            </div>
            <Button
              type="submit"
              variant="accent"
              size="sm"
              icon={<Plus strokeWidth={1.75} />}
              loading={adding}
              disabled={adding}
            >
              Add domain
            </Button>
          </form>
        )}

        {loadError && <Callout tone="danger">{loadError}</Callout>}

        <div className="-mx-5 -mb-5 border-t border-divider">
          {loading ? (
            <p className="px-5 py-6 text-ui text-slate-500">Loading custom domains…</p>
          ) : domains.length === 0 ? (
            <div className="flex flex-col items-center px-5 py-8 text-center">
              <Globe className="h-5 w-5 text-slate-500" strokeWidth={1.5} aria-hidden="true" />
              <p className="mt-3 text-sm font-medium text-white">No custom domains</p>
            </div>
          ) : (
            <ul className="divide-y divide-divider">
              {domains.map((domain) => (
                <DomainRow
                  key={domain.id}
                  domain={domain}
                  canWrite={canWrite}
                  verifying={verifying === domain.id}
                  onVerify={() => handleVerify(domain)}
                  onRemove={() => setPendingRemove(domain)}
                />
              ))}
            </ul>
          )}
        </div>
      </div>

      <ConfirmDialog
        open={canWrite && pendingRemove !== null}
        title="Remove custom domain"
        description={
          pendingRemove
            ? `The page stops being served on ${pendingRemove.hostname} at once. Visitors who bookmarked it, and browsers subscribed to notifications there, lose it.`
            : undefined
        }
        confirmLabel="Remove domain"
        tone="danger"
        loading={removing}
        onConfirm={handleRemove}
        onCancel={() => !removing && setPendingRemove(null)}
      />
    </Panel>
  );
}

interface DomainRowProps {
  domain: StatusPageDomain;
  canWrite: boolean;
  verifying: boolean;
  onVerify: () => void;
  onRemove: () => void;
}

function DomainRow({ domain, canWrite, verifying, onVerify, onRemove }: DomainRowProps) {
  const state = statusPageDomainState(domain);
  const served = domain.status === 'verified';
  return (
    <li className="space-y-3 px-5 py-4">
      <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            {served ? (
              <a
                href={domain.url}
                target="_blank"
                rel="noopener noreferrer"
                className="truncate font-mono text-sm font-medium text-white hover:underline"
              >
                {domain.hostname}
              </a>
            ) : (
              <span className="truncate font-mono text-sm font-medium text-white">{domain.hostname}</span>
            )}
            <Pill tone={statusPageDomainTone(state)} size="xs" dot>
              {STATUS_PAGE_DOMAIN_LABEL[state]}
            </Pill>
            {domain.primary && (
              <Pill tone="neutral" size="xs">
                Canonical
              </Pill>
            )}
          </div>
          <p className="mt-1 text-xs text-slate-500">{statusPageDomainHint(state)}</p>
          <p className="mt-0.5 flex flex-wrap gap-x-3 text-xs text-slate-500">
            <span>
              Last checked <RelativeTime value={domain.last_checked_at} fallback="never" />
            </span>
            {domain.verified_at && <span>Verified {formatDateTime(domain.verified_at)}</span>}
            {domain.failing_since && <span>Failing since {formatDateTime(domain.failing_since)}</span>}
            {domain.expires_at && <span>Removed if unverified on {formatDateTime(domain.expires_at)}</span>}
          </p>
        </div>
        {canWrite && (
          <div className="flex flex-shrink-0 items-center gap-2">
            <IconButton
              icon={<RefreshCw strokeWidth={1.75} />}
              label={`Verify ${domain.hostname} now`}
              onClick={onVerify}
              loading={verifying}
              spinIcon
              disabled={verifying}
            />
            <IconButton icon={<Trash2 strokeWidth={1.75} />} label={`Remove ${domain.hostname}`} onClick={onRemove} danger />
          </div>
        )}
      </div>

      {domain.last_error && (
        <Callout tone={state === 'pending' || state === 'superseded' ? 'info' : 'warning'} title="Last check">
          {domain.last_error}
        </Callout>
      )}

      <div className="glass-well overflow-x-auto">
        <table className="w-full min-w-[32rem] text-left text-xs">
          <thead className="text-slate-500">
            <tr>
              <th scope="col" className="px-3 py-2 font-medium">Type</th>
              <th scope="col" className="px-3 py-2 font-medium">Name</th>
              <th scope="col" className="px-3 py-2 font-medium">Value</th>
              <th scope="col" className="px-3 py-2 font-medium">Found</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-divider">
            {domain.records.map((record) => (
              <RecordRow key={record.type} record={record} />
            ))}
          </tbody>
        </table>
      </div>
    </li>
  );
}

function RecordRow({ record }: { record: StatusPageDomainRecord }) {
  const clipboard = useClipboard();
  const copyCell = (value: string, key: string, what: string) => (
    <div className="flex items-center gap-1.5">
      <code className="min-w-0 break-all font-mono text-slate-200">{value}</code>
      <IconButton
        size="sm"
        icon={clipboard.copiedKey === key ? <Check strokeWidth={1.75} /> : <Copy strokeWidth={1.75} />}
        label={`Copy ${record.type} record ${what}`}
        onClick={() => void clipboard.copy(value, key)}
      />
    </div>
  );
  return (
    <tr>
      <td className="px-3 py-2 font-mono text-slate-300">{record.type}</td>
      <td className="px-3 py-2">{copyCell(record.name, 'name', 'name')}</td>
      <td className="px-3 py-2">{copyCell(record.value, 'value', 'value')}</td>
      <td className="px-3 py-2">
        {record.found === null ? (
          <span className="inline-flex items-center gap-1 text-slate-500">
            <Minus className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
            Not checked
          </span>
        ) : record.found ? (
          <span className="inline-flex items-center gap-1 text-emerald-400">
            <Check className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
            Yes
          </span>
        ) : (
          <span className="inline-flex items-center gap-1 text-rose-400">
            <X className="h-3.5 w-3.5" strokeWidth={1.75} aria-hidden="true" />
            Missing
          </span>
        )}
      </td>
    </tr>
  );
}
