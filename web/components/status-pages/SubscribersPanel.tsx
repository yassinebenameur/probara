'use client';

import { useCallback, useEffect, useState } from 'react';
import { Download, Mail, Trash2 } from 'lucide-react';
import type { StatusPage, StatusPageSubscriber, StatusPageSubscriberListResponse } from '@/lib/types';
import {
  exportStatusPageSubscribers,
  getStatusPageSubscribers,
  removeStatusPageSubscriber,
  updateStatusPage,
} from '@/lib/api';
import { formatCount, formatDateTime, pluralize } from '@/lib/format';
import { errorMessage } from '@/lib/errors';
import {
  AUTO_PUBLISH_DELAY_MINUTES,
  SUBSCRIBER_STATE_LABELS,
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
import Pagination from '@/components/ui/Pagination';
import Pill from '@/components/ui/Pill';
import Switch from '@/components/ui/Switch';

interface SubscribersPanelProps {
  statusPage: StatusPage;
}

const PAGE_SIZE = 25;

/**
 * Email subscribers of a status page: whether visitors may subscribe, the
 * automatic publication of outages, and the subscriber list (remove, CSV
 * export). Subscribing is the visitors' own double opt-in on the public
 * page; nobody can be added here.
 */
export default function SubscribersPanel({ statusPage }: SubscribersPanelProps) {
  const canWrite = useCanWrite();
  const { showToast } = useToast();
  const [data, setData] = useState<StatusPageSubscriberListResponse | null>(null);
  const [page, setPage] = useState(1);
  const [loadError, setLoadError] = useState('');
  const [saving, setSaving] = useState<'' | 'subscriptions' | 'auto'>('');
  const [exporting, setExporting] = useState(false);
  const [pendingRemove, setPendingRemove] = useState<StatusPageSubscriber | null>(null);
  const [removing, setRemoving] = useState(false);

  const load = useCallback(async () => {
    try {
      setLoadError('');
      setData(await getStatusPageSubscribers(statusPage.id, { page, page_size: PAGE_SIZE }));
    } catch (err) {
      setLoadError(errorMessage(err, 'Failed to load subscribers'));
    }
  }, [statusPage.id, page]);

  useEffect(() => {
    load();
  }, [load]);

  // The two settings are saved from here and kept here: handing the updated
  // page back to the edit page would reset the main form's unsaved edits.
  const [settings, setSettings] = useState(statusPage.settings);
  useEffect(() => setSettings(statusPage.settings), [statusPage.settings]);
  const subscriptionsOn = Boolean(settings?.enable_email_subscriptions);
  const autoPublishOn = Boolean(settings?.auto_publish_incidents);
  const availability = subscriptionAvailability(settings, statusPage.visibility);
  const mailReady = data?.mail_ready ?? true;

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

  const counts = data?.counts;
  const totalPages = data ? Math.max(1, Math.ceil(data.total / PAGE_SIZE)) : 1;

  return (
    <Panel
      title="Subscribers"
      subtitle="Visitors subscribe on the public page and confirm their address by email. They are emailed when an incident is published, updated or resolved here, and when maintenance is scheduled."
      actions={
        data && data.total > 0 ? (
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
              <p className="text-sm font-medium text-white">
                Email subscriptions
              </p>
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
                is published here (and resolved when it recovers). Maintenance, outages a dependency explains and
                monitors already recovering are left out.
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

        {data && !data.mail_ready && (
          <Callout tone="warning" title="This installation cannot email subscribers">
            {data.mail_problems.join(' ')}
          </Callout>
        )}
        {subscriptionsOn && !availability.active && availability.reason && (
          <Callout tone="info">{availability.reason}</Callout>
        )}
        {loadError && <Callout tone="danger">{loadError}</Callout>}

        {counts && (
          <p className="text-xs text-slate-500">
            {pluralize(counts.confirmed, 'confirmed subscriber')} · {counts.pending} awaiting confirmation · limit{' '}
            {formatCount(data?.limit)}
          </p>
        )}

        <div className="-mx-5 -mb-5 border-t border-divider">
          {!data && !loadError ? (
            <p className="px-5 py-6 text-ui text-slate-500">Loading subscribers…</p>
          ) : data && data.items.length === 0 ? (
            <div className="flex flex-col items-center px-5 py-8 text-center">
              <Mail className="h-5 w-5 text-slate-500" strokeWidth={1.5} aria-hidden="true" />
              <p className="mt-3 text-sm font-medium text-white">No subscribers yet</p>
            </div>
          ) : (
            <ul className="divide-y divide-divider">
              {data?.items.map((sub) => (
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
                    {canWrite && (
                      <IconButton
                        icon={<Trash2 strokeWidth={1.75} />}
                        label={`Remove ${sub.email}`}
                        onClick={() => setPendingRemove(sub)}
                        danger
                      />
                    )}
                  </div>
                </li>
              ))}
            </ul>
          )}
          {data && data.total > PAGE_SIZE && (
            <Pagination variant="footer" page={page} totalPages={totalPages} onPageChange={setPage} />
          )}
        </div>
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
