'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import Link from 'next/link';
import { Send } from 'lucide-react';
import type { NotificationDelivery, NotificationDeliveryListResponse } from '@/lib/types';
import { formatDateTime, formatDateTimeFull } from '@/lib/format';
import { deliveryOutcomeHint, deliveryOutcomeLabel, deliveryOutcomeTone } from '@/lib/status-tones';
import { deliveryAlertSubject, deliveryEventLabel, deliveryPathLabel, deliveryRecipients, deliveryStepLabel, deliveryTarget } from '@/lib/deliveries';
import { alertFocusHref } from '@/lib/alert-links';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import EmptyState from '@/components/ui/EmptyState';
import Panel from '@/components/ui/Panel';
import Pill from '@/components/ui/Pill';
import TableSkeleton from '@/components/ui/TableSkeleton';

const PAGE_SIZE = 20;

interface DeliveryHistoryProps {
  /** Reads one page; the panel refetches when it changes identity. */
  load: (page: number, pageSize: number) => Promise<NotificationDeliveryListResponse>;
  /** alert: one alert's attempts across channels; channel: one channel's attempts across alerts. */
  scope: 'alert' | 'channel';
  title?: string;
  subtitle?: string;
}

// The notification delivery history (GET …/deliveries), newest first: every
// attempt, its outcome and the sanitized provider error. Shared by the
// alerts page's focused view and the alert channel page.
export default function DeliveryHistory({ load, scope, title = 'Deliveries', subtitle }: DeliveryHistoryProps) {
  const [page, setPage] = useState(1);
  const [data, setData] = useState<NotificationDeliveryListResponse | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const request = useRef(0);

  const fetchPage = useCallback(async () => {
    const id = ++request.current;
    setLoading(true);
    try {
      const res = await load(page, PAGE_SIZE);
      if (id !== request.current) return;
      setData(res);
      setError('');
    } catch (err: any) {
      if (id !== request.current) return;
      setError(err?.message || 'Failed to load deliveries');
    } finally {
      if (id === request.current) setLoading(false);
    }
  }, [load, page]);

  useEffect(() => {
    void fetchPage();
  }, [fetchPage]);

  const items = data?.items ?? [];
  const totalPages = Math.max(1, Math.ceil((data?.total ?? 0) / PAGE_SIZE));

  return (
    <Panel
      title={title}
      subtitle={subtitle ?? 'Every attempt to deliver a notification, kept 90 days.'}
      actions={
        <Button variant="ghost" size="xs" onClick={() => void fetchPage()} loading={loading && data !== null}>
          Refresh
        </Button>
      }
    >
      {error && (
        <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={() => void fetchPage()}>Retry</Button>}>
          {error}
        </Callout>
      )}
      {data === null && !error ? (
        <TableSkeleton bare rows={2} columns={3} label="Loading deliveries" />
      ) : items.length === 0 && !error ? (
        <EmptyState
          variant="inline"
          headingLevel={3}
          icon={<Send strokeWidth={1.5} />}
          title="No deliveries recorded"
          description={
            scope === 'alert'
              ? 'No channel was notified about this alert in the last 90 days.'
              : 'This channel has not been sent an alert in the last 90 days.'
          }
        />
      ) : (
        items.length > 0 && (
          <div className="-mx-5 overflow-x-auto">
            <table className="data-table text-sm">
              <thead>
                <tr>
                  <th>Time</th>
                  <th>{scope === 'alert' ? 'Channel' : 'Alert'}</th>
                  <th className="hidden sm:table-cell">Event</th>
                  <th>Outcome</th>
                  <th className="hidden md:table-cell">Detail</th>
                </tr>
              </thead>
              <tbody>
                {items.map((d) => (
                  <DeliveryRow key={d.id} delivery={d} scope={scope} />
                ))}
              </tbody>
            </table>
          </div>
        )
      )}
      {totalPages > 1 && (
        <div className="mt-4 flex items-center justify-center gap-2">
          <Button variant="ghost" size="sm" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={page === 1}>
            Previous
          </Button>
          <span className="text-ui tabular-nums text-slate-400">
            Page {page} of {totalPages}
          </span>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            disabled={page === totalPages}
          >
            Next
          </Button>
        </div>
      )}
    </Panel>
  );
}

function DeliveryRow({ delivery: d, scope }: { delivery: NotificationDelivery; scope: 'alert' | 'channel' }) {
  const recipients = deliveryRecipients(d);
  const step = deliveryStepLabel(d.delivery_key);
  const event = `${deliveryEventLabel(d.event_type)}${step ? ` (${step})` : ''} · attempt ${d.attempt}`;
  const target = deliveryTarget(d);
  const detail = [recipients, d.error].filter(Boolean).join(' — ');
  return (
    <tr>
      <td className="whitespace-nowrap text-ui text-slate-300" title={formatDateTimeFull(d.created_at)}>
        {formatDateTime(d.created_at)}
        <div className="mt-0.5 text-xs text-slate-500 sm:hidden">{event}</div>
      </td>
      <td className="max-w-[16rem]">
        {scope === 'alert' ? (
          <span className="block truncate text-white" title={target.title}>
            {target.title}
            {target.via && <span className="ml-1 text-xs text-slate-500">{target.via}</span>}
          </span>
        ) : (
          <Link
            href={alertFocusHref(d.alert_id)}
            className="block truncate text-white transition-colors hover:text-cyan-300"
            title={deliveryAlertSubject(d)}
          >
            {deliveryAlertSubject(d)}
          </Link>
        )}
        <div className="mt-0.5 text-xs text-slate-500">{deliveryPathLabel(d.path)}</div>
      </td>
      <td className="hidden whitespace-nowrap text-ui text-slate-400 sm:table-cell">{event}</td>
      <td>
        <span title={deliveryOutcomeHint(d.outcome)}>
          <Pill tone={deliveryOutcomeTone(d.outcome)} size="xs" dot>
            {deliveryOutcomeLabel(d.outcome)}
          </Pill>
        </span>
        {detail && (
          <div className="mt-1 max-w-[16rem] truncate text-xs text-slate-500 md:hidden" title={detail}>
            {detail}
          </div>
        )}
      </td>
      <td className="hidden max-w-md md:table-cell">
        {detail ? (
          <span className="line-clamp-2 break-words text-xs text-slate-400" title={detail}>
            {detail}
          </span>
        ) : (
          <span className="text-xs text-slate-600">—</span>
        )}
      </td>
    </tr>
  );
}
