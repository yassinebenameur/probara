'use client';

import { Fragment, useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import type { DashboardGroup, DashboardProblemMonitor, DashboardRange } from '@/lib/types';
import { getDashboardGroupSparkline } from '@/lib/api';
import { pluralize } from '@/lib/format';
import { groupAttentionCount, groupHealth, sortServiceGroups } from '@/lib/dashboard-view-model';
import { flashStyle, useRowFlash, type FlashTone } from '@/lib/useRowFlash';
import { TONE_TEXT } from '@/lib/status-tones';
import { FOCUS_RING } from '@/components/ui/focus';
import { GroupDetail, GroupTile } from './GroupTile';

interface Props {
  groupTags: string[];
  groups: DashboardGroup[];
  range: DashboardRange;
  filterTags: string[];
  /** The problem list: the real state of members that carry one. */
  monitorStates?: DashboardProblemMonitor[];
}

// Group tags are chosen in Settings → General (tenant `dashboard_group_tags`).
const GROUP_TAGS_HREF = '/settings?tab=general';

const UNGROUPED_KEY = '__ungrouped';
const keyOf = (group: DashboardGroup) => group.tag ?? UNGROUPED_KEY;

const HEALTH_FLASH: Record<string, FlashTone> = {
  down: 'danger',
  degraded: 'warning',
  up: 'success',
};

const LINK = `rounded text-sm text-cyan-300 transition-colors hover:text-cyan-200 hover:underline ${FOCUS_RING}`;

// One line instead of a panel: with no real group to show, a full card would
// only repeat the fleet uptime under "Ungrouped".
function CompactPrompt({ message, linkLabel }: { message: string; linkLabel: string }) {
  return (
    <section className="glass flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 px-5 py-4">
      <h2 className="text-title font-semibold text-white">Service groups</h2>
      <p className="text-sm text-slate-400">{message}</p>
      <Link href={GROUP_TAGS_HREF} className={LINK}>
        {linkLabel}
      </Link>
    </section>
  );
}

/**
 * Health per team or service as a full-width grid of compact tiles, worst
 * first, so twenty groups take a few rows and a broken one is never below
 * the fold. Opening a tile inserts its members as a full-width row right
 * under the tile's row (grid-flow-dense backfills the cells it leaves).
 */
export default function ServiceGroupsPanel({ groupTags, groups, range, filterTags, monitorStates }: Props) {
  const states = useMemo(
    () => new Map((monitorStates ?? []).map((monitor) => [monitor.monitor_id, monitor])),
    [monitorStates],
  );
  const [openKey, setOpenKey] = useState<string | null>(null);
  // Sparklines by group, fetched once per group while the range and scope hold.
  const [sparklines, setSparklines] = useState<Record<string, (number | null)[]>>({});

  const scopeKey = `${range}|${filterTags.join(',')}`;
  useEffect(() => {
    setSparklines({});
  }, [scopeKey]);

  // Groups with no monitors (stale tags, empty Ungrouped) carry no health to
  // show; the tag itself stays visible and removable in Settings → General.
  const visibleGroups = useMemo(
    () => sortServiceGroups(groups.filter((g) => g.monitor_count > 0), states),
    [groups, states],
  );
  const health = useMemo(() => new Map(visibleGroups.map((g) => [keyOf(g), groupHealth(g, states)])), [visibleGroups, states]);
  const attentionGroups = visibleGroups.filter((g) => groupAttentionCount(g, states) > 0).length;
  const anyDown = visibleGroups.some((g) => health.get(keyOf(g)) === 'down');
  const { flashClass } = useRowFlash(visibleGroups, keyOf, (g) => health.get(keyOf(g)));

  const openGroup = openKey ? visibleGroups.find((g) => keyOf(g) === openKey) : undefined;
  const openGroupTag = openGroup?.tag;
  const needsSparkline = Boolean(openGroup) && openKey !== null && !(openKey in sparklines);

  useEffect(() => {
    if (!needsSparkline || openKey === null) return;
    let cancelled = false;
    getDashboardGroupSparkline({ group: openGroupTag ?? null, range, tags: filterTags })
      .then((resp) => resp.buckets)
      .catch(() => [] as (number | null)[])
      .then((buckets) => {
        if (!cancelled) setSparklines((current) => ({ ...current, [openKey]: buckets }));
      });
    return () => {
      cancelled = true;
    };
  }, [needsSparkline, openKey, openGroupTag, range, filterTags]);

  if (groupTags.length === 0) {
    return <CompactPrompt message="Group monitors by tag to see health per team or service." linkLabel="Choose group tags" />;
  }

  if (visibleGroups.every((g) => g.tag == null)) {
    return <CompactPrompt message="No monitor carries a group tag yet." linkLabel="Edit group tags" />;
  }

  return (
    <section className="glass min-w-0" aria-labelledby="service-groups-title">
      <div className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-1 px-5 py-4">
        <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
          <h2 id="service-groups-title" className="text-title font-semibold text-white">
            Service groups
          </h2>
          <p className="text-ui tabular-nums text-slate-500">
            {pluralize(visibleGroups.length, 'group')}
            {attentionGroups > 0 && (
              <>
                {' · '}
                <span className={anyDown ? TONE_TEXT.danger : TONE_TEXT.warning}>
                  {attentionGroups} need{attentionGroups === 1 ? 's' : ''} attention
                </span>
              </>
            )}
          </p>
        </div>
        <Link href={GROUP_TAGS_HREF} className={`${LINK} text-ui`}>
          Edit group tags
        </Link>
      </div>
      <div className="grid grid-flow-row-dense gap-2 px-3 pb-3 [grid-template-columns:repeat(auto-fill,minmax(15rem,1fr))]">
        {visibleGroups.map((g) => {
          const key = keyOf(g);
          const open = key === openKey;
          const detailId = `group-detail-${key.replace(/[^a-zA-Z0-9_-]/g, '-')}`;
          const groupHealthValue = health.get(key) ?? 'unknown';
          return (
            <Fragment key={key}>
              <GroupTile
                group={g}
                health={groupHealthValue}
                states={states}
                open={open}
                detailId={detailId}
                onToggle={() => setOpenKey(open ? null : key)}
                className={flashClass(key)}
                style={flashStyle(HEALTH_FLASH[groupHealthValue] ?? 'neutral')}
              />
              {open && (
                <GroupDetail
                  id={detailId}
                  group={g}
                  states={states}
                  sparkline={sparklines[key] ?? null}
                  onClose={() => setOpenKey(null)}
                  className="col-span-full"
                />
              )}
            </Fragment>
          );
        })}
      </div>
    </section>
  );
}
