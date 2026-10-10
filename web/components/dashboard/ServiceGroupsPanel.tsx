'use client';

import { useMemo } from 'react';
import Link from 'next/link';
import type { DashboardGroup, DashboardProblemMonitor, DashboardRange } from '@/lib/types';
import GroupRow from './GroupRow';

interface Props {
  groupTags: string[];
  groups: DashboardGroup[];
  range: DashboardRange;
  filterTags: string[];
  /** The problem list: the real state of members that carry one (see GroupRow). */
  monitorStates?: DashboardProblemMonitor[];
}

// Group tags are chosen in Settings → General (tenant `dashboard_group_tags`).
const GROUP_TAGS_HREF = '/settings?tab=general';

// One line instead of a panel: with no real group to show, a full card would
// only repeat the fleet uptime under "Ungrouped".
function CompactPrompt({ message, linkLabel }: { message: string; linkLabel: string }) {
  return (
    <section className="glass flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 px-5 py-4">
      <h3 className="text-title font-semibold text-white">Service groups</h3>
      <p className="text-sm text-slate-400">{message}</p>
      <Link href={GROUP_TAGS_HREF} className="text-sm text-cyan-300 transition-colors hover:text-cyan-200 hover:underline">
        {linkLabel}
      </Link>
    </section>
  );
}

export default function ServiceGroupsPanel({ groupTags, groups, range, filterTags, monitorStates }: Props) {
  const states = useMemo(
    () => new Map((monitorStates ?? []).map((monitor) => [monitor.monitor_id, monitor])),
    [monitorStates],
  );

  if (groupTags.length === 0) {
    return <CompactPrompt message="Group monitors by tag to see health per team or service." linkLabel="Choose group tags" />;
  }

  // Groups with no monitors (stale tags, empty Ungrouped) carry no health to
  // show; the tag itself stays visible and removable in Settings → General.
  const visibleGroups = groups.filter((g) => g.monitor_count > 0);

  if (visibleGroups.every((g) => g.tag == null)) {
    return <CompactPrompt message="No monitor carries a group tag yet." linkLabel="Edit group tags" />;
  }

  return (
    <section className="glass min-w-0 overflow-hidden">
      <div className="px-5 py-4">
        <h3 className="text-title font-semibold text-white">Service groups</h3>
      </div>
      <div className="divide-y divide-divider">
        {visibleGroups.map((g) => (
          <GroupRow key={g.tag ?? '__ungrouped'} group={g} range={range} filterTags={filterTags} states={states} />
        ))}
      </div>
    </section>
  );
}
