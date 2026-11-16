'use client';

import { useCallback, useState } from 'react';
import { Send } from 'lucide-react';
import type { NotificationDeliveryPage } from '@/lib/types';
import { getChannelDeliveries } from '@/lib/api';
import { useAsyncData } from '@/lib/useAsyncData';
import { DELIVERY_FILTERS, deliveryFilterOutcomes, type DeliveryFilter } from '@/lib/deliveries';
import Button from '@/components/ui/Button';
import Callout from '@/components/ui/Callout';
import EmptyState from '@/components/ui/EmptyState';
import Pagination from '@/components/ui/Pagination';
import Panel from '@/components/ui/Panel';
import SegmentedControl from '@/components/ui/SegmentedControl';
import TableSkeleton from '@/components/ui/TableSkeleton';
import DeliveryTable from '@/components/deliveries/DeliveryTable';

const PAGE_SIZE = 25;

const EMPTY: NotificationDeliveryPage = { items: [], page: 1, page_size: PAGE_SIZE, total: 0 };

// The channel page's recent deliveries: its attempts across alerts, test
// sends included, newest first.
export default function ChannelDeliveries({ channelId }: { channelId: string }) {
  const [filter, setFilter] = useState<DeliveryFilter>('all');
  const [page, setPage] = useState(1);
  const load = useCallback(
    () => getChannelDeliveries(channelId, { outcome: deliveryFilterOutcomes(filter), page, page_size: PAGE_SIZE }),
    [channelId, filter, page]
  );
  const { data, loading, error, reload } = useAsyncData<NotificationDeliveryPage>(load, EMPTY, 'Failed to load deliveries');
  const totalPages = Math.max(1, Math.ceil(data.total / PAGE_SIZE));

  return (
    <Panel
      title="Recent deliveries"
      subtitle="Every attempt to deliver an alert notification through this channel, kept 90 days."
      actions={
        <SegmentedControl
          options={DELIVERY_FILTERS}
          value={filter}
          onChange={(next) => {
            setFilter(next);
            setPage(1);
          }}
          label="Show deliveries"
          size="xs"
        />
      }
    >
      {error ? (
        <Callout tone="danger" action={<Button variant="ghost" size="xs" onClick={reload}>Retry</Button>}>
          {error}
        </Callout>
      ) : loading && data.items.length === 0 ? (
        <TableSkeleton bare rows={3} columns={4} label="Loading deliveries" />
      ) : data.items.length === 0 ? (
        <EmptyState
          variant="inline"
          headingLevel={3}
          icon={<Send strokeWidth={1.5} />}
          title={filter === 'all' ? 'No deliveries yet' : 'No matching deliveries'}
          description={
            filter === 'all'
              ? 'Nothing was sent through this channel in the last 90 days. A test notification shows here too.'
              : 'Nothing with this outcome in the last 90 days.'
          }
        />
      ) : (
        <div className="-mx-5 space-y-3">
          <DeliveryTable items={data.items} scope="channel" />
          <Pagination page={page} totalPages={totalPages} onPageChange={setPage} />
        </div>
      )}
    </Panel>
  );
}
