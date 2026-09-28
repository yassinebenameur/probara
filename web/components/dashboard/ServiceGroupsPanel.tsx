'use client';

import Link from 'next/link';
import { ArrowRight } from 'lucide-react';
import type { DashboardGroup, DashboardRange } from '@/lib/types';
import Button from '@/components/ui/Button';
import GroupRow from './GroupRow';

interface Props {
  groupTags: string[];
  groups: DashboardGroup[];
  range: DashboardRange;
  filterTags: string[];
}

export default function ServiceGroupsPanel({ groupTags, groups, range, filterTags }: Props) {
  if (groupTags.length === 0) {
    return (
      <section className="glass min-w-0 p-5">
        <h3 className="text-[15px] font-semibold text-white">Service groups</h3>
        <p className="mt-1 max-w-sm text-sm text-slate-400">
          Group monitors by tag to see health by team or service.
        </p>
        <Button
          variant="ghost"
          asChild
          className="mt-4"
          iconTrailing={<ArrowRight className="h-4 w-4" strokeWidth={1.75} />}
        >
          <Link href="/settings">Choose group tags</Link>
        </Button>
      </section>
    );
  }

  // Groups with no monitors (stale tags, empty Ungrouped) carry no health to
  // show; the tag itself stays visible and removable in Settings → General.
  const visibleGroups = groups.filter((g) => g.monitor_count > 0);

  if (visibleGroups.length === 0) {
    return (
      <section className="glass min-w-0 p-5">
        <h3 className="text-[15px] font-semibold text-white">Service groups</h3>
        <p className="mt-1 text-sm text-slate-400">No monitors carry the dashboard group tags yet.</p>
      </section>
    );
  }

  return (
    <section className="glass min-w-0 overflow-hidden">
      <div className="flex items-center justify-between gap-2 px-5 py-4">
        <h3 className="text-[15px] font-semibold text-white">Service groups</h3>
        <Link
          href="/monitors"
          className="inline-flex items-center gap-1.5 text-[13px] text-cyan-300 transition-colors hover:text-cyan-200"
        >
          View all monitors <ArrowRight className="h-3.5 w-3.5" strokeWidth={1.75} />
        </Link>
      </div>
      <div className="divide-y divide-divider">
        {visibleGroups.map((g) => (
          <GroupRow key={g.tag ?? '__ungrouped'} group={g} range={range} filterTags={filterTags} />
        ))}
      </div>
    </section>
  );
}
