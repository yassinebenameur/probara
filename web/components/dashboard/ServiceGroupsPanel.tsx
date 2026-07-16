'use client';

import Link from 'next/link';
import { ArrowRight } from 'lucide-react';
import type { DashboardGroup, DashboardRange } from '@/lib/types';
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
      <section className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-8 text-center">
        <h3 className="text-sm font-medium text-white">Service groups</h3>
        <p className="mx-auto mt-3 max-w-sm text-sm text-slate-400">
          Group monitors by tag to see health by team or service.
        </p>
        <Link
          href="/settings#dashboard-groups"
          className="mt-4 inline-flex items-center gap-2 rounded-lg border border-cyan-500/40 bg-cyan-500/10 px-3.5 py-2 text-sm font-medium text-cyan-200 transition-colors hover:bg-cyan-500/20"
        >
          Choose group tags <ArrowRight className="h-4 w-4" />
        </Link>
      </section>
    );
  }

  // Named groups always render (count 0 = stale tag, visible by design).
  // Ungrouped (tag === null) is suppressed when monitor_count === 0.
  const visibleGroups = groups.filter((g) => g.tag !== null || g.monitor_count > 0);

  if (visibleGroups.length === 0) {
    return (
      <section className="rounded-xl border border-white/[0.06] bg-slate-900/50 p-6">
        <h3 className="text-sm font-medium text-white">Service groups</h3>
        <p className="mt-3 text-sm text-slate-500">No groups match the current tag filter.</p>
      </section>
    );
  }

  return (
    <section className="min-w-0 rounded-xl border border-white/[0.06] bg-slate-900/50">
      <div className="flex items-center justify-between border-b border-white/[0.04] px-5 py-3.5">
        <h3 className="text-sm font-medium text-white">Service groups</h3>
        <Link href="/monitors" className="inline-flex items-center gap-1.5 text-xs text-cyan-400 transition-colors hover:text-cyan-300">
          View all monitors <ArrowRight className="h-3.5 w-3.5" />
        </Link>
      </div>
      <div className="divide-y divide-white/[0.04]">
        {visibleGroups.map((g) => (
          <GroupRow key={g.tag ?? '__ungrouped'} group={g} range={range} filterTags={filterTags} />
        ))}
      </div>
    </section>
  );
}
