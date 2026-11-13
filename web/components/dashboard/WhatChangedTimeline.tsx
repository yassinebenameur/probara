'use client';

import EmptyState from '@/components/ui/EmptyState';
import Pill from '@/components/ui/Pill';
import ScrollList from '@/components/ui/ScrollList';
import TableSkeleton from '@/components/ui/TableSkeleton';
import { RelativeTime } from '@/components/ui/RelativeTime';
import Link from 'next/link';
import { ActivityTimelineItem } from '@/lib/dashboard-view-model';
import { flashStyle } from '@/lib/useRowFlash';
import { LIST_HEIGHT, ROW_LINK, TEXT_LINK } from '@/components/dashboard/layout';
import { ReasonText } from '@/components/dashboard/ReasonText';
import { SectionCard, SectionLoadError } from '@/components/dashboard/SectionCard';

export function TimelineRow({ item, flashClass }: { item: ActivityTimelineItem; flashClass: string }) {
  const body = (
    <>
      <div className="flex min-w-0 items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2">
          <span className="min-w-0 truncate text-sm font-medium text-white">{item.title}</span>
          <Pill tone={item.tone} size="xs" className="shrink-0">
            {item.event}
          </Pill>
        </div>
        <RelativeTime value={item.occurredAt} className="shrink-0 text-xs tabular-nums text-slate-500" />
      </div>
      {(item.reason || item.detail) && (
        <div className="mt-0.5 flex min-w-0 items-center justify-between gap-3 text-xs">
          <ReasonText message={item.reason} />
          {item.detail && (
            <span className="ml-auto shrink-0 tabular-nums text-slate-500">
              {item.detail}
              {item.detailAt && (
                <>
                  {' '}
                  <RelativeTime value={item.detailAt} />
                </>
              )}
            </span>
          )}
        </div>
      )}
    </>
  );
  const style = flashStyle(item.tone);
  return item.href ? (
    <Link href={item.href} className={`${ROW_LINK} ${flashClass}`} style={style}>
      {body}
    </Link>
  ) : (
    <div className={`px-5 py-2.5 ${flashClass}`} style={style}>
      {body}
    </div>
  );
}

export function WhatChangedTimeline({
  items,
  loading,
  loadFailed,
  onRetry,
  rangeLabel,
  flashClass,
}: {
  items: ActivityTimelineItem[];
  loading: boolean;
  /** The activity never loaded: show the failure, not "No changes". */
  loadFailed: boolean;
  onRetry: () => void;
  rangeLabel: string;
  flashClass: (id: string) => string;
}) {
  return (
    <SectionCard
      title="What changed"
      action={
        <Link href="/alerts" className={TEXT_LINK}>
          Alert history
        </Link>
      }
    >
      {loadFailed ? (
        <SectionLoadError onRetry={onRetry} />
      ) : loading ? (
        <TableSkeleton rows={3} columns={1} bare label="Loading recent activity" />
      ) : items.length > 0 ? (
        <ScrollList label="Recent changes" className={`min-h-0 flex-1 ${LIST_HEIGHT}`}>
          <div className="divide-y divide-divider">
            {items.map((item) => (
              <TimelineRow key={item.id} item={item} flashClass={flashClass(item.id)} />
            ))}
          </div>
        </ScrollList>
      ) : (
        <EmptyState
          variant="inline"
          headingLevel={3}
          title="No changes"
          description={`Failing monitors and alerts in the ${rangeLabel.toLowerCase()} appear here.`}
        />
      )}
    </SectionCard>
  );
}
