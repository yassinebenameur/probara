'use client';

import { useState } from 'react';
import Link from 'next/link';
import { ChevronDown, AlertTriangle, CheckCircle2, MinusCircle } from 'lucide-react';
import type { DashboardGroup, DashboardGroupMember, DashboardProblemMonitor, DashboardRange } from '@/lib/types';
import { getDashboardGroupSparkline } from '@/lib/api';
import { formatUptime, pluralize } from '@/lib/format';
import { problemMonitorStatus } from '@/lib/dashboard-view-model';
import {
  MONITOR_STATUS_LABEL,
  MONITOR_STATUS_TONE,
  TONE_TEXT,
  UPTIME_TONE_TEXT,
  uptimeTone,
} from '@/lib/status-tones';
import GroupSparkline from './GroupSparkline';

interface Props {
  group: DashboardGroup;
  range: DashboardRange;
  filterTags: string[];
  /** Problem-list rows by monitor id: they carry the real state (current_state). */
  states?: ReadonlyMap<string, DashboardProblemMonitor>;
}

type MemberLike = Pick<DashboardGroupMember, 'monitor_id' | 'current_status'>;

// A member's current_status is the API's state-to-status mapping, which reads
// degraded as "success"; a member on the problem list has its real state.
function memberStatus(member: MemberLike | null | undefined, states?: ReadonlyMap<string, DashboardProblemMonitor>) {
  return problemMonitorStatus(member && (states?.get(member.monitor_id) ?? member));
}

function statusIcon(group: DashboardGroup, states?: ReadonlyMap<string, DashboardProblemMonitor>) {
  if (group.monitor_count === 0) {
    return <MinusCircle className="h-4 w-4 text-slate-500" strokeWidth={1.75} />;
  }
  if (group.attention_count > 0) {
    const worstDown = memberStatus(group.worst_member, states) === 'down';
    return <AlertTriangle className={`h-4 w-4 ${worstDown ? 'text-rose-400' : 'text-amber-400'}`} strokeWidth={1.75} />;
  }
  return <CheckCircle2 className="h-4 w-4 text-emerald-400" strokeWidth={1.75} />;
}

// No status means no check yet (or paused, which the API does not
// distinguish), shown as "No data".
function MemberStatus({ member, states }: { member: MemberLike; states?: ReadonlyMap<string, DashboardProblemMonitor> }) {
  const display = memberStatus(member, states);
  return (
    <span className={`text-right text-xs ${TONE_TEXT[MONITOR_STATUS_TONE[display]]}`}>
      {MONITOR_STATUS_LABEL[display]}
    </span>
  );
}

// Healthy uptime stays neutral; warn/bad take the shared tone, so a value has
// the same colour here as in the hero and on the monitors list.
function uptimeClass(pct: number | null | undefined): string {
  const tone = uptimeTone(pct);
  return tone === 'good' ? 'text-slate-200' : UPTIME_TONE_TEXT[tone];
}

export default function GroupRow({ group, range, filterTags, states }: Props) {
  const [open, setOpen] = useState(false);
  const [sparkline, setSparkline] = useState<(number | null)[] | null>(null);
  const [loading, setLoading] = useState(false);

  const label = group.tag ?? 'Ungrouped';
  const linkHref = group.tag ? `/monitors?tag=${encodeURIComponent(group.tag)}` : '/monitors';

  async function toggle() {
    const next = !open;
    setOpen(next);
    if (next && sparkline === null && !loading) {
      setLoading(true);
      try {
        const resp = await getDashboardGroupSparkline({
          group: group.tag,
          range,
          tags: filterTags,
        });
        setSparkline(resp.buckets);
      } catch {
        setSparkline([]);
      } finally {
        setLoading(false);
      }
    }
  }

  return (
    <div>
      <button
        type="button"
        onClick={toggle}
        aria-expanded={open}
        className="group grid w-full grid-cols-[auto_1fr_auto_auto_auto] items-center gap-4 px-5 py-3 text-left transition-colors hover:bg-well focus:outline-none focus-visible:bg-well focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-cyan-500/50"
      >
        {statusIcon(group, states)}
        <span className="min-w-0 truncate text-sm font-medium text-white">{label}</span>
        <span className="text-xs tabular-nums text-slate-500">
          {pluralize(group.monitor_count, 'monitor')}
        </span>
        <span className={`w-16 text-right text-sm font-medium tabular-nums ${group.monitor_count === 0 ? 'text-slate-500' : uptimeClass(group.uptime)}`}>
          {group.monitor_count === 0 ? '—' : formatUptime(group.uptime)}
        </span>
        <ChevronDown className={`h-4 w-4 text-slate-500 transition-all group-hover:text-slate-300 ${open ? 'rotate-180' : ''}`} strokeWidth={1.75} />
      </button>

      {open && (
        <div className="bg-well px-5 pb-4 pt-1">
          {group.members.length === 0 ? (
            <p className="py-2 text-sm text-slate-500">No monitors in this group.</p>
          ) : (
            <ul className="space-y-1.5 py-2">
              {group.members.map((m) => (
                <li key={m.monitor_id} className="grid grid-cols-[minmax(0,1fr)_4.5rem_4.5rem] items-center gap-3 text-sm">
                  <Link
                    href={`/monitors/${m.monitor_id}`}
                    className="truncate text-slate-200 transition-colors hover:text-cyan-300"
                  >
                    {m.monitor_name}
                  </Link>
                  <span className={`text-right tabular-nums ${uptimeClass(m.uptime)}`}>
                    {formatUptime(m.uptime)}
                  </span>
                  <MemberStatus member={m} states={states} />
                </li>
              ))}
            </ul>
          )}

          <div className="mt-3 flex items-center justify-between">
            {loading ? (
              <span className="text-xs text-slate-500">Loading sparkline…</span>
            ) : sparkline && sparkline.length > 0 ? (
              <GroupSparkline buckets={sparkline} />
            ) : (
              <span className="text-xs text-slate-500">No sparkline data</span>
            )}
            <Link href={linkHref} className="text-xs text-cyan-300 transition-colors hover:text-cyan-200 hover:underline">
              View all {group.monitor_count}
            </Link>
          </div>
        </div>
      )}
    </div>
  );
}
