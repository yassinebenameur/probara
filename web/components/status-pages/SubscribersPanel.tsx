'use client';

import { useCallback, useEffect, useState } from 'react';
import { Download, Mail, Trash2 } from 'lucide-react';
import type {
  StatusPage,
  StatusPageSubscriber,
  StatusPageSubscriberListResponse,
  StatusPageSubscriberSummary,
} from '@/lib/types';
import {
  exportStatusPageSubscribers,
  getStatusPageSubscriberSummary,
  getStatusPageSubscribers,
  removeStatusPageSubscriber,
  removeStatusPageSubscribers,
  updateStatusPage,
} from '@/lib/api';
import { formatCount, formatDateTime, pluralize } from '@/lib/format';
import { errorMessage } from '@/lib/errors';
import {
  AUTO_PUBLISH_DELAY_MINUTES,
  SUBSCRIBER_STATE_LABELS,
  noticeLabel,
  noticeOutcome,
  subscriptionAvailability,
} from '@/lib/status-page-subscribers';
import { useCanWrite } from '@/components/providers/CurrentUserProvider';
import { useToast } from '@/components/ui/ToastProvider';
import { saveDownload } from '@/components/slas/SlaVisuals';
import Panel from '@/components/ui/Panel';
import Button from '@/components/ui/Button';
import IconButton from '@/components/ui/IconButton';
import Callout from '@/components/ui/Callout';
import ConfirmDialog from '@/components/ui/ConfirmDialog';
import Input from '@/components/ui/Input';
import Pagination from '@/components/ui/Pagination';
import Pill from '@/components/ui/Pill';
import Switch from '@/components/ui/Switch';

interface SubscribersPanelProps {
  statusPage: StatusPage;
}

const PAGE_SIZE = 25;

/**
 * Email subscribers of a status page: whether visitors may subscribe, the
 * automatic publication of outages, what recent notices came to, and (for
 * members with write access: addresses are personal data) the subscriber
 * list with remove, bulk remove and CSV export. Subscribing is the
 * visitors' own double opt-in on the public page; nobody can be added here.
 */
export default function SubscribersPanel({ statusPage }: SubscribersPanelProps) {
  const canWrite = useCanWrite();
  const { showToast } = useToast();
  const [summary, setSummary] = useState<StatusPageSubscriberSummary | null>(null);
  const [list, setList] = useState<StatusPageSubscriberListResponse | null>(null);
  const [page, setPage] = useState(1);
  const [loadError, setLoadError] = useState('');
  const [saving, setSaving] = useState<'' | 'subscriptions' | 'auto'>('');
  const [exporting, setExporting] = useState(false);
  const [pendingRemove, setPendingRemove] = useState<StatusPageSubscriber | null>(null);
  const [removing, setRemoving] = useState(false);
  const [bulkDomain, setBulkDomain] = useState('');
  const [bulkConfirm, setBulkConfirm] = useState('');
  const [bulkRemoving, setBulkRemoving] = useState(false);
  // The two settings are saved from here and kept here: handing the updated
  // page back to the edit page would reset the main form's unsaved edits.
  const [settings, setSettings] = useState(statusPage.settings);
  useEffect(() => setSettings(statusPage.settings), [statusPage.settings]);

  const load = useCallback(async () => {
    try {
      setLoadError('');
      setSummary(await getStatusPageSubscriberSummary(statusPage.id));
      if (canWrite) {
        setList(await getStatusPageSubscribers(statusPage.id, { page, page_size: PAGE_SIZE }));
      }
    } catch (err) {
      setLoadError(errorMessage(err, 'Failed to load subscribers'));
    }
  }, [statusPage.id, page, canWrite]);

  useEffect(() => {
    load();
  }, [load]);

  const subscriptionsOn = Boolean(settings?.enable_email_subscriptions);
  const autoPublishOn = Boolean(settings?.auto_publish_incidents);
  const availability = subscriptionAvailability(settings, statusPage.visibility);
  const mailReady = summary?.mail_ready ?? true;

  const saveSetting = async (key: 'subscriptions' | 'auto', value: boolean) => {
    try {
      setSaving(key);
      const updated = await updateStatusPage(statusPage.id, {
        settings: key === 'subscriptions' ? { enable_email_subscriptions: value } : { auto_publish_incidents: value },
      });
      setSettings(updated.settings);
      showToast(
        key === 'subscriptions'
          ? value
            ? 'Email subscriptions turned on'
            : 'Email subscriptions turned off'
          : value
            ? 'Outages will be published automatically'
            : 'Automatic outage publishing turned off',
        'success'
      );
    } catch (err) {
      showToast(errorMessage(err, 'Failed to save'), 'error');
    } finally {
      setSaving('');
    }
  };

  const handleExport = async () => {
    try {
      setExporting(true);
      saveDownload(await exportStatusPageSubscribers(statusPage.id));
    } catch (err) {
      showToast(errorMessage(err, 'Failed to export subscribers'), 'error');
    } finally {
      setExporting(false);
    }
  };

  const handleRemove = async () => {
    if (!pendingRemove) return;
    try {
      setRemoving(true);
      await removeStatusPageSubscriber(statusPage.id, pendingRemove.id);
      setPendingRemove(null);
      showToast('Subscriber removed', 'success');
      await load();
    } catch (err) {
      showToast(errorMessage(err, 'Failed to remove subscriber'), 'error');
    } finally {
      setRemoving(false);
    }
  };

  // Bulk removal is typed-confirmed: the operator types the page's slug.
  const handleBulkRemove = async (event: React.FormEvent) => {
    event.preventDefault();
    if (bulkConfirm.trim() !== statusPage.slug) return;
    try {
      setBulkRemoving(true);
      const domain = bulkDomain.trim();
      const { removed } = await removeStatusPageSubscribers(statusPage.id, domain ? { domain } : { all: true });
      setBulkConfirm('');
      setBulkDomain('');
      setPage(1);
      showToast(`Removed ${pluralize(removed, 'subscriber')}`, 'success');
      await load();
    } catch (err) {
      showToast(errorMessage(err, 'Failed to remove subscribers'), 'error');
    } finally {
      setBulkRemoving(false);
    }
  };

  const counts = summary?.counts;
  const total = counts ? counts.confirmed + counts.pending : 0;
  const totalPages = list ? Math.max(1, Math.ceil(list.total / PAGE_SIZE)) : 1;

  return (
    <Panel
      title="Subscribers"
      subtitle="Visitors subscribe on the public page and confirm their address by email. They are emailed when an incident is published, updated or resolved here, and when maintenance is scheduled, starts or is cancelled."
      actions={
        canWrite && total > 0 ? (
          <Button
            variant="ghost"
            size="xs"
            icon={<Download strokeWidth={1.75} />}
            loading={exporting}
            disabled={exporting}
            onClick={handleExport}
          >
            Export CSV
          </Button>
        ) : undefined
      }
    >
      <div className="space-y-4">
        <div className="glass-well divide-y divide-divider">
          <div className="flex items-start justify-between gap-4 p-4">
            <div className="min-w-0">
              <p className="text-sm font-medium text-white">Email subscriptions</p>
              <p className="mt-0.5 text-xs text-slate-500">
                Shows a subscribe form on the public page. Public and unlisted pages only.
              </p>
            </div>
            <Switch
              checked={subscriptionsOn}
              onChange={(next) => saveSetting('subscriptions', next)}
              label="Email subscriptions"
              disabled={!canWrite || saving !== '' || (!subscriptionsOn && !mailReady)}
            />
          </div>
          <div className="flex items-start justify-between gap-4 p-4">
            <div className="min-w-0">
              <p className="text-sm font-medium text-white">Publish outages automatically</p>
              <p className="mt-0.5 text-xs text-slate-500">
                When a monitor shown on this page has been down for {AUTO_PUBLISH_DELAY_MINUTES} minutes, an incident
                titled “Service disruption” with the page’s component names is published here, and resolved when the
                monitor has stayed up for 10 minutes. Maintenance, outages a dependency explains and monitors already
                recovering are left out.
              </p>
            </div>
            <Switch
              checked={autoPublishOn}
              onChange={(next) => saveSetting('auto', next)}
              label="Publish outages automatically"
              disabled={!canWrite || saving !== ''}
            />
          </div>
        </div>

        {summary && !summary.mail_ready && (
          <Callout tone="warning" title="This installation cannot email subscribers">
            {summary.mail_problems.join(' ')}
          </Callout>
        )}
        {subscriptionsOn && !availability.active && availability.reason && (
          <Callout tone="info">{availability.reason}</Callout>
        )}
        {loadError && <Callout tone="danger">{loadError}</Callout>}

        {counts && summary && (
          <p className="text-xs text-slate-500">
            {pluralize(counts.confirmed, 'confirmed subscriber')} · {counts.pending} awaiting confirmation · limit{' '}
            {formatCount(summary.limit)}
          </p>
        )}

        {summary && summary.recent.length > 0 && (
          <div>
            <h3 className="mb-2 text-ui font-medium text-slate-300">Recent notices</h3>
            <ul className="glass-well divide-y divide-divider">
              {summary.recent.map((o) => {
                const outcome = noticeOutcome(o);
                return (
                  <li key={o.id} className="px-3.5 py-2.5">
                    <div className="flex flex-wrap items-baseline justify-between gap-x-3">
                      <span className="min-w-0 truncate text-ui text-white">{noticeLabel(o)}</span>
                      <span className="text-xs text-slate-500">{formatDateTime(o.created_at)}</span>
                    </div>
                    <p className={`mt-0.5 text-xs ${outcome.problem ? 'text-amber-300' : 'text-slate-500'}`}>
                      {outcome.text}
                      {o.last_error ? ` — ${o.last_error}` : ''}
                    </p>
                  </li>
                );
              })}
            </ul>
          </div>
        )}

        {!canWrite ? (
          <p className="text-xs text-slate-500">Subscriber addresses are shown to members who can edit this page.</p>
        ) : (
          <>
            {total > 0 && (
              <form onSubmit={handleBulkRemove} className="glass-well space-y-3 p-4">
                <div>
                  <p className="text-sm font-medium text-white">Remove in bulk</p>
                  <p className="mt-0.5 text-xs text-slate-500">
                    Remove every subscriber, or only those at one domain (after a flood of sign-ups, for example).
                  </p>
                </div>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Input
                    label="Domain (empty: everyone)"
                    value={bulkDomain}
                    onChange={(event) => setBulkDomain(event.target.value)}
                    placeholder="example.com"
                    maxLength={253}
                  />
                  <Input
                    label={`Type “${statusPage.slug}” to confirm`}
                    value={bulkConfirm}
                    onChange={(event) => setBulkConfirm(event.target.value)}
                    autoComplete="off"
                  />
                </div>
                <Button
                  type="submit"
                  variant="danger"
                  size="sm"
                  icon={<Trash2 strokeWidth={1.75} />}
                  loading={bulkRemoving}
                  disabled={bulkRemoving || bulkConfirm.trim() !== statusPage.slug}
                >
                  {bulkDomain.trim() ? `Remove subscribers at ${bulkDomain.trim()}` : 'Remove every subscriber'}
                </Button>
              </form>
            )}

            <div className="-mx-5 -mb-5 border-t border-divider">
              {!list && !loadError ? (
                <p className="px-5 py-6 text-ui text-slate-500">Loading subscribers…</p>
              ) : list && list.items.length === 0 ? (
                <div className="flex flex-col items-center px-5 py-8 text-center">
                  <Mail className="h-5 w-5 text-slate-500" strokeWidth={1.5} aria-hidden="true" />
                  <p className="mt-3 text-sm font-medium text-white">No subscribers yet</p>
                </div>
              ) : (
                <ul className="divide-y divide-divider">
                  {list?.items.map((sub) => (
                    <li key={sub.id} className="flex flex-col gap-2 px-5 py-3 sm:flex-row sm:items-center sm:justify-between">
                      <div className="min-w-0">
                        <p className="truncate font-mono text-ui text-white">{sub.email}</p>
                        <p className="mt-0.5 text-xs text-slate-500">
                          {sub.confirmed_at
                            ? `Confirmed ${formatDateTime(sub.confirmed_at)}`
                            : `Asked ${formatDateTime(sub.requested_at)}`}
                        </p>
                      </div>
                      <div className="flex flex-shrink-0 items-center gap-2">
                        <Pill tone={sub.state === 'confirmed' ? 'success' : 'neutral'} size="xs" dot>
                          {SUBSCRIBER_STATE_LABELS[sub.state]}
                        </Pill>
                        <IconButton
                          icon={<Trash2 strokeWidth={1.75} />}
                          label={`Remove ${sub.email}`}
                          onClick={() => setPendingRemove(sub)}
                          danger
                        />
                      </div>
                    </li>
                  ))}
                </ul>
              )}
              {list && list.total > PAGE_SIZE && (
                <Pagination variant="footer" page={page} totalPages={totalPages} onPageChange={setPage} />
              )}
            </div>
          </>
        )}
      </div>

      <ConfirmDialog
        open={canWrite && pendingRemove !== null}
        title="Remove subscriber"
        description={
          pendingRemove
            ? `${pendingRemove.email} will receive no more emails from this page. The address can subscribe again from the public page.`
            : undefined
        }
        confirmLabel="Remove subscriber"
        loading={removing}
        onConfirm={handleRemove}
        onCancel={() => !removing && setPendingRemove(null)}
      />
    </Panel>
  );
}
