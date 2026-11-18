'use client';

import { useCallback, useState } from 'react';
import type { NotificationDeliveryPage } from '@/lib/types';
import { getAlertDeliveries } from '@/lib/api';
import { useAsyncData } from '@/lib/useAsyncData';
import { pluralize } from '@/lib/format';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import Pagination from '@/components/ui/Pagination';
import TableSkeleton from '@/components/ui/TableSkeleton';
import DeliveryTable from '@/components/deliveries/DeliveryTable';

const PAGE_SIZE = 50;

const EMPTY: NotificationDeliveryPage = { items: [], page: 1, page_size: PAGE_SIZE, total: 0 };

// An alert's Notifications section in its expanded feed row: every attempt to
// deliver it, across its channels, in the order they happened. Mounted only
// while the row is open, so the fetch happens on demand.
export default function AlertDeliveries({ alertId, headingClassName }: { alertId: string; headingClassName: string }) {
  const [page, setPage] = useState(1);
  const load = useCallback(() => getAlertDeliveries(alertId, { page, page_size: PAGE_SIZE }), [alertId, page]);
  const { data, loading, error, reload } = useAsyncData<NotificationDeliveryPage>(load, EMPTY, 'Failed to load notifications');
  const totalPages = Math.max(1, Math.ceil(data.total / PAGE_SIZE));

  return (
    <div className="min-w-0 sm:col-span-2 xl:col-span-3">
      <h3 className={`mb-2.5 ${headingClassName}`}>
        Notifications
        {data.total > 0 && <span className="ml-1.5 font-normal normal-case tracking-normal">· {pluralize(data.total, 'attempt')}</span>}
      </h3>
      {error ? (
        <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={reload}>Retry</Button>}>
          {error}
        </Callout>
      ) : loading && data.items.length === 0 ? (
        <TableSkeleton bare rows={2} columns={4} label="Loading notifications" />
      ) : data.items.length === 0 ? (
        <p className="text-sm text-slate-400">
          No notification was attempted for this alert in the last 90 days (none routed, still within an escalation delay, or suppressed).
        </p>
      ) : (
        <div className="space-y-3">
          <DeliveryTable items={data.items} scope="alert" />
          <Pagination page={page} totalPages={totalPages} onPageChange={setPage} />
        </div>
      )}
    </div>
  );
}
