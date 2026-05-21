'use client';

import type { DashboardGroup } from '@/lib/types';

interface Props {
  group: DashboardGroup;
  range: '24h' | '7d' | '30d' | '90d' | '365d';
  filterTags: string[];
}

export default function GroupRow({ group }: Props) {
  return (
    <div className="px-5 py-3 text-sm text-slate-400">
      {group.tag ?? 'Ungrouped'} — {group.monitor_count} monitor{group.monitor_count === 1 ? '' : 's'} — {group.uptime.toFixed(2)}%
    </div>
  );
}
